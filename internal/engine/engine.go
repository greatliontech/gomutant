// Package engine is gomutant's Go-language layer: it loads a Go tree,
// resolves target and oracle symbols through the type checker, and hashes
// symbol bodies.
//
// A symbol reference is "<import-path>.<Ident>" or, for methods,
// "<import-path>.<Receiver>.<Method>". The import path is matched against
// loaded package paths (longest match), never parsed lexically, so import
// paths containing dots resolve correctly. The grammar is shared with the
// tools gomutant composes with (a freshness engine, a spec binder), so one
// symbol string names the same declaration everywhere.
package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/greatliontech/gomutant/internal/contextio"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
	"strconv"
)

// Tree is a loaded Go tree: a single module, or a workspace whose go.work
// members are all in scope.
type Tree struct {
	pkgs            []*packages.Package
	env             []string
	importProcessor importProcessor
	// sourceDigests pins every loaded Go file's content AT THE PARSE
	// (the loader's ParseFile hook hashes the exact bytes it parses,
	// so pin and token offsets are one observation — no window).
	// candidateCatalog re-reads source from disk and refuses on a
	// digest mismatch: a mid-run tree edit surfaces as the contracted
	// target-local drift refusal instead of splicing mutations at
	// stale offsets into moved bytes (REQ-exec-quiescence's
	// generation-time pin).
	sourceDigests map[string][sha256.Size]byte
	// dir is the absolute tree root Load resolved, kept to reconcile
	// Fset-absolute file paths back to the tree-relative paths callers speak.
	dir string
	// build lazily indexes the loaded build for ephemeral validation.
	build buildSet
	// directiveView lazily carries the directive seam's product
	// (DirectiveCoverage): the profile re-keying map and the
	// known-unsound files.
	directiveOnce sync.Once
	directiveView DirectiveCoverageView
}

// Load loads the tree rooted at dir, including test packages: the module
// alone, or every go.work member when the tree is a workspace — package
// patterns are module-scoped, so nested modules would otherwise vanish from
// symbol resolution. A load failure is an error, never an empty tree.
func Load(dir string) (*Tree, error) {
	return load(dir, processExecutionSupported)
}

func load(dir string, executionSupported bool) (*Tree, error) {
	return loadContext(context.Background(), dir, Selection{}, executionSupported)
}

// LoadContext is Load with caller-owned cancellation.
func LoadContext(ctx context.Context, dir string) (*Tree, error) {
	return loadContext(ctx, dir, Selection{}, processExecutionSupported)
}

// LoadContextSelection is LoadContext under a declared build selection:
// the selection rewrites the tree's one frozen environment before
// anything reads it, so package loading, target discovery, constraint
// matching, oracle spawns, and the measurement pins all see the same
// selection by construction — a tag-gated oracle becomes visible end to
// end exactly as an untagged one.
func LoadContextSelection(ctx context.Context, dir string, sel Selection) (*Tree, error) {
	return loadContext(ctx, dir, sel, processExecutionSupported)
}

func loadContext(ctx context.Context, dir string, sel Selection, executionSupported bool) (*Tree, error) {
	return loadContextWith(ctx, dir, sel, executionSupported, packages.Load)
}

func loadContextWith(ctx context.Context, dir string, sel Selection, executionSupported bool, loadPackages func(*packages.Config, ...string) ([]*packages.Package, error)) (*Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !executionSupported {
		return nil, fmt.Errorf("gomutant: mutation execution supports Unix and Windows hosts")
	}
	env, err := sel.applyEnv(GoEnv(dir))
	if err != nil {
		return nil, err
	}
	// Provenance first: a skewed frontend must refuse before it
	// parses anything. The one sample also serves the build-events
	// floor.
	sampledToolchain, err := toolchainProvenance(ctx, dir, env)
	if err != nil {
		return nil, err
	}
	if err := toolchainSupportsBuildEvents(sampledToolchain); err != nil {
		return nil, err
	}
	members, err := workspaceMembersContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	var pkgs []*packages.Package
	// The content pins are taken FROM THE PARSE: the loader hands this
	// hook the exact bytes it parses, so the digest and the token
	// offsets are one observation — no window exists in which an edit
	// can pin new bytes against old offsets (REQ-exec-quiescence's
	// generation-time pin). candidateCatalog compares its re-read
	// against these.
	digests := &sourceDigestCapture{pins: map[string][sha256.Size]byte{}}
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg := &packages.Config{
			Context: ctx,
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
				packages.NeedForTest | packages.NeedEmbedFiles,
			Dir:       filepath.Join(dir, m),
			Env:       env,
			Tests:     true,
			ParseFile: digests.parseAndPin,
		}
		loaded, err := loadPackages(cfg, "./...")
		if err != nil {
			return nil, fmt.Errorf("loading Go packages in %s: %w", m, err)
		}
		pkgs = append(pkgs, loaded...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Deterministic candidate order regardless of load order.
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving tree root %s: %w", dir, err)
	}
	return &Tree{pkgs: pkgs, dir: abs, env: append([]string(nil), env...), sourceDigests: digests.snapshot()}, nil
}

// SourceDriftError names a source file whose on-disk content no longer
// matches the bytes the load parsed: mutation offsets against it would
// splice garbage, so the target refuses locally with the drift named —
// never a generated mutant from moved bytes, never a campaign abort
// (REQ-exec-quiescence's target-local drift refusal).
type SourceDriftError struct {
	Path string
}

func (e *SourceDriftError) Error() string {
	return "source changed since load: " + e.Path
}

// PackagesHealthyContext reports the first load or type error any
// loaded package carries. packages.Load errors only on driver failure,
// so a tree with an unparseable file still "loads" - with its
// declarations silently missing from the partial syntax. A consumer
// whose judgment destroys state on a symbol's absence must refuse an
// unhealthy load: absence under errors is indistinguishable from a
// rename.
func (t *Tree) PackagesHealthyContext(ctx context.Context) error {
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(pkg.Errors) > 0 {
			return fmt.Errorf("package %s did not load cleanly: %s", pkg.ID, pkg.Errors[0].Msg)
		}
	}
	return nil
}

func basePackagePath(pkg *packages.Package) string {
	if pkg.ForTest != "" {
		return pkg.ForTest
	}
	return pkg.PkgPath
}

// PackageContext returns the module and package directories used by a test
// binary for pkgPath.
func (t *Tree) PackageContext(pkgPath string) (moduleDir, packageDir string, err error) {
	return t.PackageContextContext(context.Background(), pkgPath)
}

// PackageContextContext is PackageContext with cancellation while scanning loaded packages.
func (t *Tree) PackageContextContext(ctx context.Context, pkgPath string) (moduleDir, packageDir string, err error) {
	var fallback *packages.Package
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if basePackagePath(pkg) != pkgPath || pkg.Module == nil || len(pkg.GoFiles) == 0 {
			continue
		}
		if pkg.PkgPath == pkgPath && pkg.ForTest == "" {
			return pkg.Module.Dir, filepath.Dir(pkg.GoFiles[0]), nil
		}
		if fallback == nil {
			fallback = pkg
		}
	}
	if fallback != nil {
		return fallback.Module.Dir, filepath.Dir(fallback.GoFiles[0]), nil
	}
	return "", "", fmt.Errorf("package %s has no loaded module context", pkgPath)
}

// workspaceMembers returns the tree's Go module directories, relative to
// dir: the go.work members when a workspace file is present, the root alone
// otherwise. Package patterns are module-scoped even in workspace mode, so
// every surface that walks "./..." must iterate the members itself or nested
// modules silently vanish.
func workspaceMembers(dir string) ([]string, error) {
	return workspaceMembersContext(context.Background(), dir)
}

func workspaceMembersContext(ctx context.Context, dir string) ([]string, error) {
	b, err := contextio.ReadFile(ctx, filepath.Join(dir, "go.work"))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{"."}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading go.work: %w", err)
	}
	wf, err := modfile.ParseWork("go.work", b, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.work: %w", err)
	}
	var members []string
	for _, u := range wf.Use {
		clean := filepath.Clean(u.Path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			// A member outside the tree would make the same commit measure
			// differently per machine: hermeticity is refused away, never
			// silently bent.
			return nil, fmt.Errorf("go.work member %q escapes the tree; members must lie within it", u.Path)
		}
		members = append(members, clean)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("go.work declares no members")
	}
	return members, nil
}

// toolchainSupportsBuildEvents refuses toolchains below go1.24: build-failure
// classification reads the test harness's build-fail events, which older
// test2json streams do not emit — an uncompilable mutant would fall through
// to the differential probe and score as a kill, the forbidden flattering
// direction. A version string that does not parse (a devel toolchain) is
// modern by construction and passes THE FLOOR — though the
// provenance guard upstream refuses unidentifiable versions before
// this check runs, so the leniency is defense in depth for the pure
// function's own contract, not a reachable load-path behavior. The probe reads the PATH binary, which
// GOTOOLCHAIN=auto could switch UP for the actual test runs when the target's
// go.mod directs a newer toolchain — that shape refuses loudly where the runs
// would in fact have been sound: the chosen direction is the conservative
// one, never a silent kill on an event-less stream.
func toolchainSupportsBuildEvents(version string) error {
	if major, minor, ok := parseGoVersion(version); ok && belowBuildEventFloor(major, minor) {
		return fmt.Errorf("gomutant: toolchain %q is below go1.24: build-failure classification requires the harness's build-fail events", version)
	}
	return nil
}

// belowBuildEventFloor reports whether a parsed toolchain version predates
// the go1.24 test2json build-fail events.
func belowBuildEventFloor(major, minor int) bool {
	return major < 1 || (major == 1 && minor < 24)
}

// parseGoVersion extracts the goMAJOR.MINOR pair from a `go version` line;
// ok is false for devel and otherwise unparseable strings.
func parseGoVersion(version string) (major, minor int, ok bool) {
	for _, field := range strings.Fields(version) {
		if !strings.HasPrefix(field, "go") {
			continue
		}
		rest := strings.TrimPrefix(field, "go")
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) < 2 {
			continue
		}
		majorText := parts[0]
		minorText := parts[1]
		if i := strings.IndexFunc(minorText, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
			minorText = minorText[:i]
		}
		majorValue, majorErr := strconv.Atoi(majorText)
		minorValue, minorErr := strconv.Atoi(minorText)
		if majorErr != nil || minorErr != nil {
			continue
		}
		return majorValue, minorValue, true
	}
	return 0, 0, false
}

// GoEnv returns the complete process environment with workspace mode pinned for
// a spawned go command or package load:
// the tree's own go.work when it has one, explicitly off otherwise. The go
// command discovers workspace files by walking UP, so an enclosing
// repository's workspace would otherwise leak into fixture trees that are
// not its members and refuse their "./..." patterns.
func GoEnv(dir string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "GOWORK") {
			env = append(env, entry)
		}
	}
	work := filepath.Join(dir, "go.work")
	if _, err := os.Stat(work); err == nil {
		if abs, aerr := filepath.Abs(work); aerr == nil {
			work = abs
		}
		return append(env, "GOWORK="+work)
	}
	return append(env, "GOWORK=off")
}

// GoEnv returns the environment used by this tree's package loads and test
// processes.
func (t *Tree) GoEnv() []string { return append([]string(nil), t.env...) }

// ModuleDirs enumerates the distinct root directories of the loaded
// packages' main modules — the tree's version-selection anchors.
func (t *Tree) ModuleDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	for _, pkg := range t.pkgs {
		if pkg.Module == nil || pkg.Module.Dir == "" || !pkg.Module.Main || seen[pkg.Module.Dir] {
			continue
		}
		seen[pkg.Module.Dir] = true
		dirs = append(dirs, pkg.Module.Dir)
	}
	sort.Strings(dirs)
	return dirs
}

// sourceDigestCapture pins loaded file content at the parse itself:
// the loader's ParseFile hook hands over the exact bytes it parses,
// so the pin and the parse are one observation of the file
// (REQ-exec-quiescence). Concurrent by the loader's contract.
type sourceDigestCapture struct {
	mu   sync.Mutex
	pins map[string][sha256.Size]byte
}

// parseAndPin reproduces the loader's default parse exactly
// (comments kept, all errors reported, ast.Object resolution KEPT —
// the loader's own default promises it and downstream AST walks may
// read Ident.Obj) and records the parsed bytes' digest.
func (c *sourceDigestCapture) parseAndPin(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
	if filepath.Ext(filename) == ".go" {
		sum := sha256.Sum256(src)
		c.mu.Lock()
		c.pins[filename] = sum
		c.mu.Unlock()
	}
	return parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
}

// snapshot hands the pinned set over once loading completes.
func (c *sourceDigestCapture) snapshot() map[string][sha256.Size]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	pins := make(map[string][sha256.Size]byte, len(c.pins))
	for path, sum := range c.pins {
		pins[path] = sum
	}
	return pins
}
