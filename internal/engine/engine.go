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
	"os/exec"
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
	env := goworkEnv(dir)
	var pkgs []*packages.Package
	for _, m := range members {
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo,
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
	return &Tree{pkgs: pkgs, dir: abs}, nil
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

// goworkEnv pins workspace mode for a spawned go command or package load:
// the tree's own go.work when it has one, explicitly off otherwise. The go
// command discovers workspace files by walking UP, so an enclosing
// repository's workspace would otherwise leak into fixture trees that are
// not its members and refuse their "./..." patterns.
func goworkEnv(dir string) []string {
	work := filepath.Join(dir, "go.work")
	if _, err := os.Stat(work); err == nil {
		if abs, aerr := filepath.Abs(work); aerr == nil {
			work = abs
		}
		return append(os.Environ(), "GOWORK="+work)
	}
	return append(os.Environ(), "GOWORK=off")
}

// Toolchain reports the identity of the go command the engine invokes in dir
// — "GOVERSION GOOS/GOARCH" — under the same GOWORK pinning as every other
// invocation. Records pin this identity because the same body under the same
// oracle kills differently across toolchains and platforms
// (REQ-result-record). It is the exec'd toolchain, deliberately not this
// binary's runtime version: the oracle runs under the former.
func Toolchain(dir string) (string, error) {
	cmd := exec.Command("go", "env", "GOVERSION", "GOOS", "GOARCH")
	cmd.Dir = dir
	cmd.Env = goworkEnv(dir)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolving toolchain identity: %w", err)
	}
	f := strings.Fields(string(out))
	if len(f) != 3 {
		return "", fmt.Errorf("unexpected go env output %q", out)
	}
	return f[0] + " " + f[1] + "/" + f[2], nil
}
