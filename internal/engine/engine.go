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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// Tree is a loaded Go tree: a single module, or a workspace whose go.work
// members are all in scope.
type Tree struct {
	pkgs []*packages.Package
	env  []string
	// dir is the absolute tree root Load resolved, kept to reconcile
	// Fset-absolute file paths back to the tree-relative paths callers speak.
	dir string
}

// Load loads the tree rooted at dir, including test packages: the module
// alone, or every go.work member when the tree is a workspace — package
// patterns are module-scoped, so nested modules would otherwise vanish from
// symbol resolution. A load failure is an error, never an empty tree.
func Load(dir string) (*Tree, error) {
	members, err := workspaceMembers(dir)
	if err != nil {
		return nil, err
	}
	env := GoEnv(dir)
	var pkgs []*packages.Package
	for _, m := range members {
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
				packages.NeedForTest,
			Dir:   filepath.Join(dir, m),
			Env:   env,
			Tests: true,
		}
		loaded, err := packages.Load(cfg, "./...")
		if err != nil {
			return nil, fmt.Errorf("loading Go packages in %s: %w", m, err)
		}
		pkgs = append(pkgs, loaded...)
	}
	// Deterministic candidate order regardless of load order.
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ID < pkgs[j].ID })
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving tree root %s: %w", dir, err)
	}
	return &Tree{pkgs: pkgs, dir: abs, env: append([]string(nil), env...)}, nil
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
	var fallback *packages.Package
	for _, pkg := range t.pkgs {
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
	b, err := os.ReadFile(filepath.Join(dir, "go.work"))
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
