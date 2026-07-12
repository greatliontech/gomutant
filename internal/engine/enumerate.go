package engine

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"
)

// DeclaredSymbols returns the resolver symbol of every top-level function
// and method declared in the tree's non-test, non-generated source files,
// sorted — the whole-tree discovery surface (REQ-target-producers). Test
// files are oracles, never targets; generated bodies have nothing
// hand-written to strengthen a test against, so neither yields a target.
func (t *Tree) DeclaredSymbols() []string {
	seen := map[string]bool{}
	for _, pkg := range t.pkgs {
		pkgPath := basePackagePath(pkg)
		for _, f := range pkg.Syntax {
			name := pkg.Fset.Position(f.Pos()).Filename
			if strings.HasSuffix(name, "_test.go") || ast.IsGenerated(f) {
				continue
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if sym := declSymbol(pkgPath, fn); sym != "" {
					seen[sym] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestsOf returns the symbols of pkgPath's top-level Test functions — its
// in-package and external test variants together — sorted: the derived
// default oracle of a target in that package (REQ-target-default).
func (t *Tree) TestsOf(pkgPath string) []string {
	seen := map[string]bool{}
	for _, pkg := range t.pkgs {
		if basePackagePath(pkg) != pkgPath {
			continue
		}
		for _, f := range pkg.Syntax {
			name := pkg.Fset.Position(f.Pos()).Filename
			if !strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				if runnableTest(pkg, fn) {
					seen[pkgPath+"."+fn.Name.Name] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ValidateOracle rejects symbols that are not runnable tests or that name more
// than one source declaration across in-package and external test variants.
func (t *Tree) ValidateOracle(symbols []string) error {
	seen := map[string]bool{}
	for _, symbol := range symbols {
		if seen[symbol] {
			return fmt.Errorf("oracle repeats test identity %s", symbol)
		}
		seen[symbol] = true
		pkgPath, name := t.PackageOf(symbol)
		if pkgPath == "" || name == "" || strings.Contains(name, ".") {
			return fmt.Errorf("oracle %s does not name a top-level test", symbol)
		}
		runnable := false
		for _, candidate := range t.TestsOf(pkgPath) {
			if candidate == symbol {
				runnable = true
				break
			}
		}
		if !runnable {
			return fmt.Errorf("oracle %s is not a runnable test", symbol)
		}
		declarations := map[string]bool{}
		for _, pkg := range t.pkgs {
			if basePackagePath(pkg) != pkgPath {
				continue
			}
			for _, file := range pkg.Syntax {
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Recv != nil || fn.Name.Name != name || !runnableTest(pkg, fn) {
						continue
					}
					pos := pkg.Fset.Position(fn.Pos())
					declarations[fmt.Sprintf("%s:%d", pos.Filename, pos.Offset)] = true
				}
			}
		}
		if len(declarations) != 1 {
			return fmt.Errorf("oracle %s is ambiguous across test package variants", symbol)
		}
	}
	return nil
}

// PackageOf splits a resolver symbol into its import path and the
// package-local remainder, resolved against the loaded packages (longest
// match) — "" when no loaded package matches.
func (t *Tree) PackageOf(symbol string) (pkg, rest string) {
	return t.splitSymbol(symbol)
}

// runnableTest reports whether fn is a test go test would run in an ordinary
// invocation: a Test function or a Fuzz target's seed-corpus run — the name
// prefix followed by a non-lowercase rune and the exact harness signature.
// A helper that merely starts with "Test" (a lowercase continuation, extra
// parameters, a return value) never runs, so admitting it would derive an
// oracle whose pattern executes nothing and every mutant would "survive" an
// empty run. TestMain is the harness, not a test: its *testing.M parameter
// fails the signature rule.
func runnableTest(pkg *packages.Package, fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	var prefix, param string
	switch {
	case strings.HasPrefix(name, "Test"):
		prefix, param = "Test", "T"
	case strings.HasPrefix(name, "Fuzz"):
		prefix, param = "Fuzz", "F"
	default:
		return false
	}
	if rest := name[len(prefix):]; rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsLower(r) {
			return false
		}
	}
	obj, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok {
		return false
	}
	sig := obj.Signature()
	if sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return false
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	o := named.Obj()
	return o.Pkg() != nil && o.Pkg().Path() == "testing" && o.Name() == param
}
