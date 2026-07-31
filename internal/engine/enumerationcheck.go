package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// VerifyTestEnumerationContext proves the derived test enumeration fresh
// against the tree on disk. The package loader's snapshot has been observed
// lagging the filesystem under rapid successive invocations — test functions
// written moments before a run were absent from the derived oracle while the
// same run's evidence saw the current tree — and a lagging enumeration is a
// silent coverage cap recorded as evidence: derived-oracle pins compare
// equal against a set that never contained the new tests. This check parses
// the package's on-disk _test.go files directly — every on-disk test file
// the effective build configuration selects, the snapshot's files re-read
// and re-matched alike — under a syntactic reproduction of the
// runnable-test shape (Test/Fuzz name, exactly one *testing.T or *testing.F
// parameter, the testing import resolved through each file's own import
// declarations, aliases and dot-imports included), and requires the result
// to equal the derived set exactly. Any disagreement is a refusal naming
// the differing tests: measuring against a lagging enumeration would record
// under-covered evidence as truth. Residual divergences of the syntactic
// predicate from the type-checked one (pathological shadowing of a bound
// testing name) refuse loudly rather than guessing.
func (t *Tree) VerifyTestEnumerationContext(ctx context.Context, pkgPath string, derived []string) error {
	_, packageDir, err := t.PackageContextContext(ctx, pkgPath)
	if err != nil {
		return err
	}
	matcher, err := t.buildMatchContext(ctx, packageDir)
	if err != nil {
		return fmt.Errorf("verify test enumeration of %s: %w", pkgPath, err)
	}
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		return fmt.Errorf("verify test enumeration of %s: %w", pkgPath, err)
	}
	parsed := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(packageDir, entry.Name())
		// Every on-disk file's constraints are evaluated under the tree's
		// effective build configuration — snapshot-present files included,
		// because a constraint edit after the load is exactly a lag this
		// check exists to catch: the stale snapshot still counts the file's
		// tests while the test binary excludes them.
		match, matchErr := matcher.MatchFile(packageDir, entry.Name())
		if matchErr != nil {
			return fmt.Errorf("verify test enumeration of %s: %s: %w", pkgPath, entry.Name(), matchErr)
		}
		if !match {
			continue
		}
		names, parseErr := parseTestFunctionNames(path)
		if parseErr != nil {
			return fmt.Errorf("verify test enumeration of %s: %w", pkgPath, parseErr)
		}
		for _, name := range names {
			parsed[pkgPath+"."+name] = true
		}
	}
	derivedSet := map[string]bool{}
	for _, symbol := range derived {
		derivedSet[symbol] = true
	}
	var missing, extra []string
	for symbol := range parsed {
		if !derivedSet[symbol] {
			missing = append(missing, symbol)
		}
	}
	for symbol := range derivedSet {
		if !parsed[symbol] {
			extra = append(extra, symbol)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	var parts []string
	if len(missing) != 0 {
		parts = append(parts, "on disk but not enumerated: "+strings.Join(missing, ", "))
	}
	if len(extra) != 0 {
		parts = append(parts, "enumerated but not on disk: "+strings.Join(extra, ", "))
	}
	return fmt.Errorf("derived test enumeration of %s lags the tree (%s); reload before measuring", pkgPath, strings.Join(parts, "; "))
}

// buildMatchContext derives the constraint-evaluation context from the
// tree's effective build configuration — the one its package loads and test
// processes resolve — rather than the host defaults: GOOS, GOARCH,
// CGO_ENABLED, and the -tags of GOFLAGS come from `go env` under the tree's
// environment, because a persisted `go env -w` value or a GOFLAGS tag
// changes which files the test binary compiles.
func (t *Tree) buildMatchContext(ctx context.Context, dir string) (build.Context, error) {
	cmd := exec.CommandContext(ctx, "go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOVERSION")
	cmd.Dir = dir
	cmd.Env = t.env
	out, err := cmd.Output()
	if err != nil {
		return build.Context{}, fmt.Errorf("resolve effective build configuration: %w", err)
	}
	var values struct {
		GOOS       string
		GOARCH     string
		CGOEnabled string `json:"CGO_ENABLED"`
		GOFLAGS    string
		GOVERSION  string
	}
	if err := json.Unmarshal(out, &values); err != nil {
		return build.Context{}, fmt.Errorf("resolve effective build configuration: %w", err)
	}
	matcher := build.Default
	matcher.GOOS = values.GOOS
	matcher.GOARCH = values.GOARCH
	matcher.CgoEnabled = values.CGOEnabled == "1"
	matcher.BuildTags = nil
	for _, flag := range strings.Fields(values.GOFLAGS) {
		// GOFLAGS accepts single- and double-dash forms, and a repeated
		// -tags is last-wins, exactly as the go command resolves it.
		if tags, ok := strings.CutPrefix(strings.TrimLeft(flag, "-"), "tags="); ok {
			matcher.BuildTags = strings.Split(tags, ",")
		}
	}
	if tags := releaseTags(values.GOVERSION); tags != nil {
		// Release tags follow the tree's toolchain, not the one that
		// compiled this binary: a //go:build go1.N constraint must be
		// evaluated against the go that builds the test binary. ToolTags
		// (goexperiment) keep the host defaults — a goexperiment-gated
		// test file under toolchain skew is the accepted residual, and it
		// refuses loudly in every case but a brand-new such file.
		matcher.ReleaseTags = tags
	}
	return matcher, nil
}

// releaseTags derives go/build release tags ("go1.1" … "go1.N") from a
// GOVERSION value like "go1.26.5", or nil when the version is unparseable
// (leaving the host defaults in place).
func releaseTags(version string) []string {
	rest, ok := strings.CutPrefix(version, "go1.")
	if !ok {
		return nil
	}
	minorText, _, _ := strings.Cut(rest, ".")
	minor := 0
	for _, r := range minorText {
		if r < '0' || r > '9' {
			minor = 0
			break
		}
		minor = minor*10 + int(r-'0')
	}
	if minor == 0 {
		return nil
	}
	tags := make([]string, 0, minor)
	for i := 1; i <= minor; i++ {
		tags = append(tags, fmt.Sprintf("go1.%d", i))
	}
	return tags
}

// parseTestFunctionNames parses one _test.go file from disk, syntax only,
// and returns the names of its top-level runnable-shaped test functions:
// Test/Fuzz prefix with an exported remainder and exactly one parameter of
// the file's testing import's T or F pointer type.
func parseTestFunctionNames(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// Resolve every name this file binds to the testing package: the
	// default name, any alias, or "." for a dot import — a file may import
	// testing more than once and a test may use any bound name.
	testingNames := map[string]bool{}
	for _, imp := range file.Imports {
		if imp.Path == nil || strings.Trim(imp.Path.Value, `"`) != "testing" {
			continue
		}
		name := "testing"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name != "_" {
			testingNames[name] = true
		}
	}
	if len(testingNames) == 0 {
		return nil, nil
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		param := runnableTestNameShape(fn.Name.Name)
		if param == "" || fn.Type.TypeParams != nil {
			continue
		}
		if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) > 1 {
			continue
		}
		if !matchesTestingParam(fn.Type.Params.List[0].Type, testingNames, param) {
			continue
		}
		if fn.Type.Results != nil && len(fn.Type.Results.List) != 0 {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	return names, nil
}

// runnableTestNameShape returns the required testing parameter type name for
// a runnable test's name ("T" for Test*, "F" for Fuzz*) or "" when the name
// is not runnable-shaped — the same name rule runnableTest applies.
func runnableTestNameShape(name string) string {
	var prefix, param string
	switch {
	case strings.HasPrefix(name, "Test"):
		prefix, param = "Test", "T"
	case strings.HasPrefix(name, "Fuzz"):
		prefix, param = "Fuzz", "F"
	default:
		return ""
	}
	if rest := name[len(prefix):]; rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsLower(r) {
			return ""
		}
	}
	return param
}

// matchesTestingParam reports whether a parameter's syntactic type denotes
// the required testing pointer type ("*<name>.T"/"*<name>.F", or "*T"/"*F"
// under a dot import) through any of the file's bound testing names.
func matchesTestingParam(expr ast.Expr, testingNames map[string]bool, param string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch inner := star.X.(type) {
	case *ast.Ident:
		return testingNames["."] && inner.Name == param
	case *ast.SelectorExpr:
		pkg, ok := inner.X.(*ast.Ident)
		return ok && testingNames[pkg.Name] && inner.Sel.Name == param
	default:
		return false
	}
}
