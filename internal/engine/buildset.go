package engine

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/greatliontech/go-x-tools/go/packages"
)

type funcDeclEntry struct {
	decl *ast.FuncDecl
	pkg  *packages.Package
}

// buildSet indexes the loaded build once: package import paths, the
// absolute Go files the build compiles, package variants by base and
// symbol path, top-level function declarations by type object, and
// direct property-runtime imports, so symbol/package lookups and
// ephemeral validation avoid repeated full-tree scans
// (REQ-exec-ephemeral's validation arm).
type buildSet struct {
	once            sync.Once
	packages        map[string]bool
	basePackages    map[string]bool
	files           map[string]bool
	filePkg         map[string]string
	pkgsByBase      map[string][]*packages.Package
	pkgsBySymPath   map[string][]*packages.Package
	funcsByObj      map[types.Object]funcDeclEntry
	propertyImports map[string]map[string]bool
	rtaOnce         sync.Once
	rtaAdj          map[types.Object][]types.Object
	rtaPkgInits     map[string][]types.Object
}

func (t *Tree) buildIndex() *buildSet {
	t.build.once.Do(func() {
		t.build.packages = make(map[string]bool, len(t.pkgs))
		t.build.basePackages = make(map[string]bool, len(t.pkgs))
		t.build.files = make(map[string]bool)
		t.build.filePkg = make(map[string]string)
		t.build.pkgsByBase = make(map[string][]*packages.Package, len(t.pkgs))
		t.build.pkgsBySymPath = make(map[string][]*packages.Package, len(t.pkgs))
		t.build.funcsByObj = make(map[types.Object]funcDeclEntry)
		t.build.propertyImports = make(map[string]map[string]bool)
		for _, pkg := range t.pkgs {
			t.build.packages[pkg.PkgPath] = true
			bp := basePackagePath(pkg)
			if !(len(pkg.GoFiles) == 1 && !strings.HasSuffix(pkg.GoFiles[0], ".go")) && bp != "" {
				t.build.basePackages[bp] = true
			}
			t.build.pkgsByBase[bp] = append(t.build.pkgsByBase[bp], pkg)
			t.build.pkgsBySymPath[pkg.PkgPath] = append(t.build.pkgsBySymPath[pkg.PkgPath], pkg)
			if trimmed, ok := strings.CutSuffix(pkg.PkgPath, "_test"); ok {
				t.build.pkgsBySymPath[trimmed] = append(t.build.pkgsBySymPath[trimmed], pkg)
			}
			for _, file := range pkg.GoFiles {
				clean := filepath.Clean(file)
				t.build.files[clean] = true
				t.build.filePkg[clean] = pkg.PkgPath
			}
			for _, f := range pkg.Syntax {
				for _, imp := range f.Imports {
					if name := propertyRuntimeNames[strings.Trim(imp.Path.Value, `"`)]; name != "" {
						if t.build.propertyImports[pkg.PkgPath] == nil {
							t.build.propertyImports[pkg.PkgPath] = map[string]bool{}
						}
						t.build.propertyImports[pkg.PkgPath][name] = true
					}
				}
				if pkg.TypesInfo == nil {
					continue
				}
				for _, decl := range f.Decls {
					fd, ok := decl.(*ast.FuncDecl)
					if !ok {
						continue
					}
					if obj := pkg.TypesInfo.Defs[fd.Name]; obj != nil {
						if _, exists := t.build.funcsByObj[obj]; !exists {
							t.build.funcsByObj[obj] = funcDeclEntry{decl: fd, pkg: pkg}
						}
					}
				}
			}
		}
	})
	return &t.build
}

func (t *Tree) pkgsByBase(pkgPath string) []*packages.Package {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().pkgsByBase[pkgPath]
}

func (t *Tree) pkgsBySymbolPath(pkgPath string) []*packages.Package {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().pkgsBySymPath[pkgPath]
}

func (t *Tree) funcDeclByObject(obj types.Object) (*ast.FuncDecl, *packages.Package, bool) {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	entry, ok := t.buildIndex().funcsByObj[obj]
	return entry.decl, entry.pkg, ok
}

func (t *Tree) propertyImportsByPath() map[string]map[string]bool {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().propertyImports
}

// HasPackage reports whether path names a loaded package import path.
func (t *Tree) HasPackage(path string) bool {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().packages[path]
}

// BuildCompilesFile reports whether the loaded build compiles the
// absolute file: a build-constraint-excluded source or a data file is
// not in any loaded package's GoFiles, so an overlay of it can never be
// exercised.
func (t *Tree) BuildCompilesFile(abs string) bool {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().files[filepath.Clean(abs)]
}

// FileImportPath reports the loaded package import path compiling the
// absolute file, empty when no loaded package does - the coverage
// profile's file keys are import-path-qualified, so classifying a
// replacement against a profile starts here.
func (t *Tree) FileImportPath(abs string) string {
	t.loadMu.Lock()
	defer t.loadMu.Unlock()
	return t.buildIndex().filePkg[filepath.Clean(abs)]
}

// LinkedTestPackagesContext reports the import paths `go test` compiles
// into testPkg's test binary — the package itself, its test variants,
// and every transitive dependency — via `go list -deps -test` under the
// tree's environment, cached per test package (the loaded build is
// immutable for the tree's lifetime, REQ-exec-quiescence). An ephemeral
// replacement of a file outside this set could never be exercised by
// the named oracle: a compiled-elsewhere file overlays cleanly, every
// test passes, and the verdict would be a false survivor — so
// validation refuses on this set before any process launches
// (REQ-exec-ephemeral).
func (t *Tree) LinkedTestPackagesContext(ctx context.Context, testPkg string) (map[string]bool, error) {
	t.linkedMu.Lock()
	set, ok := t.linked[testPkg]
	t.linkedMu.Unlock()
	if ok {
		return set, nil
	}
	// The lock is not held across the exec: concurrent probes on one
	// Tree (the MCP server) derive in parallel, and a racing duplicate
	// derivation costs one redundant go list, never a wrong set.
	// Gofresh's listing form: an answer a descendant held the pipe
	// past the policy's wait delay for is refused (ErrListingRefused —
	// a cut listing has no wholeness test), and the refusal is this
	// derivation's own error below, never a latched nil: a later spawn
	// without the holder answers (REQ-exec-go-command-runner).
	out, err := goRunner.List(ctx, t.dir, t.env, "-deps", "-test", "-f", "{{.ImportPath}}", testPkg)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// go list RAN and said no: the closure itself does not
			// resolve or build, and the established refusals — the
			// baseline probe's compiler-diagnostic path above all —
			// own that diagnosis in the spec's canonical framing, so
			// the gate stands down rather than refusing in its own
			// words (REQ-exec-ephemeral). The false-survivor risk
			// stays closed: a closure that does not build fails the
			// baseline probe before any verdict. The nil verdict is
			// cached like any other — re-execing go list on every
			// consult would bill the unresolvable state repeatedly.
			// The latch's bound: it is sound when the failure is a
			// function of the loaded source (the common class — a
			// broken closure stays broken for this Tree's lifetime),
			// and accepted for the narrow transient class (a module
			// cache mid-fetch, environment hiccups): a fresh Tree
			// re-derives, and the ephemeral gate standing down on the
			// latched nil still leaves the baseline probe to refuse
			// anything that genuinely does not build.
			t.linkedMu.Lock()
			if t.linked == nil {
				t.linked = map[string]map[string]bool{}
			}
			t.linked[testPkg] = nil
			t.linkedMu.Unlock()
			return nil, nil
		}
		// A start failure (fork/exec under memory pressure, RLIMIT) or
		// the listing form's refusal (ErrListingRefused — a descendant
		// held the pipe past the wait delay) says nothing about the
		// closure — standing down here would silently re-open the
		// unlinked-false-survivor channel when a later spawn succeeds,
		// so the derivation failure is the caller's error.
		return nil, fmt.Errorf("resolving %s's linked dependency set: %w", testPkg, err)
	}
	set = map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || line == testPkg+".test" {
			// The synthesized test main is no source package; keeping
			// it would wrongly admit a real tree package that happens
			// to carry the ".test"-suffixed import path.
			continue
		}
		set[cleanLinkedImportPath(line)] = true
	}
	t.linkedMu.Lock()
	if t.linked == nil {
		t.linked = map[string]map[string]bool{}
	}
	t.linked[testPkg] = set
	t.linkedMu.Unlock()
	return set, nil
}

func cleanLinkedImportPath(line string) string {
	if i := strings.Index(line, " ["); i >= 0 {
		// A test variant prints bracket-suffixed ("pkg_test
		// [pkg.test]"); its files belong to the plain import path
		// the build index records, so only genuinely bracketed
		// lines are cut. A real tree package that shares the
		// "<pkg>_test" import path would be indistinguishable —
		// the go tool itself claims that path for the synthesized
		// external test package — an inherent collision with no
		// discriminator, accepted.
		return line[:i]
	}
	return line
}

// warmLinkedTestPackagesContext resolves the linked dependency sets of
// multiple test packages in a single `go list -test` invocation when
// more than one package is uncached. If the batch fails with an
// ExitError (for example, when one package's closure does not build),
// it leaves the cache untouched so the caller's per-package
// LinkedTestPackagesContext loop isolates the broken closure.
func (t *Tree) warmLinkedTestPackagesContext(ctx context.Context, testPkgs []string) error {
	t.linkedMu.Lock()
	var missing []string
	seen := map[string]bool{}
	for _, p := range testPkgs {
		if p == "" || seen[p] {
			continue
		}
		if _, ok := t.linked[p]; !ok {
			seen[p] = true
			missing = append(missing, p)
		}
	}
	t.linkedMu.Unlock()
	if len(missing) <= 1 {
		return nil
	}
	args := append([]string{"-test", "-f", "{{.ForTest}}\t{{.ImportPath}}\t{{join .Deps \"\t\"}}"}, missing...)
	out, err := goRunner.List(ctx, t.dir, t.env, args...)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil
		}
		return fmt.Errorf("resolving linked dependency sets: %w", err)
	}
	wanted := make(map[string]bool, len(missing))
	for _, p := range missing {
		wanted[p] = true
	}
	sets := make(map[string]map[string]bool, len(missing))
	for _, line := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		forTest, imp := parts[0], parts[1]
		owner := ""
		switch {
		case forTest != "" && wanted[forTest]:
			owner = forTest
		case forTest == "" && wanted[imp]:
			owner = imp
		case forTest == "" && strings.HasSuffix(imp, ".test"):
			if stem := strings.TrimSuffix(imp, ".test"); wanted[stem] {
				owner = stem
			}
		}
		if owner == "" {
			continue
		}
		set := sets[owner]
		if set == nil {
			set = map[string]bool{owner: true}
			sets[owner] = set
		}
		if imp != "" && imp != owner+".test" {
			set[cleanLinkedImportPath(imp)] = true
		}
		for _, dep := range parts[2:] {
			if dep == "" || dep == owner+".test" {
				continue
			}
			set[cleanLinkedImportPath(dep)] = true
		}
	}
	t.linkedMu.Lock()
	if t.linked == nil {
		t.linked = map[string]map[string]bool{}
	}
	for _, p := range missing {
		if set := sets[p]; set != nil {
			t.linked[p] = set
		}
	}
	t.linkedMu.Unlock()
	return nil
}

// derivedOracleResult memoizes one package's expanded derivation
// (REQ-target-default) for the Tree's lifetime — the loaded build is
// immutable (REQ-exec-quiescence), and inspection faces re-derive per
// record without it.
type derivedOracleResult struct {
	oracle    []string
	stoodDown []string
}

type verifiedTestsResult struct {
	tests []string
	err   error
}

// verifiedTestsOfContext is TestsOfContext proven fresh: the
// enumeration is cross-checked against a direct parse of the package's
// on-disk test files EVERY time a package enters a derivation —
// packages with no snapshot tests included, because a test file
// written after the load is exactly the lag the check exists to catch
// (REQ-target-default's freshness clause; a snapshot-only candidacy
// was the demonstrated hole) — memoized per package for the Tree's
// lifetime. Cancellation is never memoized.
func (t *Tree) verifiedTestsOfContext(ctx context.Context, pkg string) ([]string, error) {
	t.derivedMu.Lock()
	if v, ok := t.verifiedTests[pkg]; ok {
		t.derivedMu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return v.tests, v.err
	}
	t.derivedMu.Unlock()
	tests, err := t.TestsOfContext(ctx, pkg)
	if err == nil {
		err = t.VerifyTestEnumerationContext(ctx, pkg, tests)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	t.derivedMu.Lock()
	if t.verifiedTests == nil {
		t.verifiedTests = map[string]verifiedTestsResult{}
	}
	t.verifiedTests[pkg] = verifiedTestsResult{tests: tests, err: err}
	t.derivedMu.Unlock()
	if err != nil {
		return nil, err
	}
	return tests, nil
}

// DerivedOracleContext derives a package's expanded default oracle
// (REQ-target-default): the freshness-verified runnable tests of every
// in-tree package whose test binary links pkgPath, its own included —
// a symbol's teeth can live one package up (a consumer suite
// demonstrably kills mutants the symbol's own package cannot see), and
// a derivation that stops at the package boundary mints survivors an
// existing test kills. Every loaded package's enumeration is verified
// against disk regardless of whether it contributes, so a lagging
// snapshot refuses the derivation instead of silently capping it. A
// test-bearing package whose linked closure stands down
// (LinkedTestPackagesContext's nil — the closure does not resolve or
// build) contributes nothing and is returned in stoodDown so the run
// can NAME the cap rather than silently narrowing. Memoized per
// package; cancellation is never memoized.
func (t *Tree) DerivedOracleContext(ctx context.Context, pkgPath string) (oracle, stoodDown []string, err error) {
	t.derivedMu.Lock()
	if r, ok := t.derivedOracles[pkgPath]; ok {
		t.derivedMu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// Cloned on every return: the memo's backing arrays are shared
		// across a cached Tree's concurrent campaigns, and one caller's
		// append or sort must never corrupt later derivations.
		return slices.Clone(r.oracle), slices.Clone(r.stoodDown), nil
	}
	t.derivedMu.Unlock()
	seen := map[string]bool{}
	var bases []string
	t.loadMu.Lock()
	for base := range t.buildIndex().pkgsByBase {
		if base != "" {
			seen[base] = true
			bases = append(bases, base)
		}
	}
	t.loadMu.Unlock()
	sort.Strings(bases)
	testsByBase := make(map[string][]string, len(bases))
	var testBases []string
	for _, base := range bases {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		tests, err := t.verifiedTestsOfContext(ctx, base)
		if err != nil {
			return nil, nil, err
		}
		if len(tests) == 0 {
			continue
		}
		testsByBase[base] = tests
		testBases = append(testBases, base)
	}
	if err := t.warmLinkedTestPackagesContext(ctx, testBases); err != nil {
		return nil, nil, err
	}
	linkedByBase := make(map[string]map[string]bool, len(testBases))
	for _, base := range testBases {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		linked, err := t.LinkedTestPackagesContext(ctx, base)
		if err != nil {
			return nil, nil, err
		}
		if linked == nil {
			stoodDown = append(stoodDown, base)
			continue
		}
		linkedByBase[base] = linked
	}
	sort.Strings(stoodDown)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	targets := slices.Clone(bases)
	if !seen[pkgPath] {
		targets = append(targets, pkgPath)
	}
	t.derivedMu.Lock()
	if t.derivedOracles == nil {
		t.derivedOracles = map[string]derivedOracleResult{}
	}
	for _, target := range targets {
		var targetOracle []string
		for _, base := range testBases {
			if linked := linkedByBase[base]; linked != nil && linked[target] {
				targetOracle = append(targetOracle, testsByBase[base]...)
			}
		}
		sort.Strings(targetOracle)
		t.derivedOracles[target] = derivedOracleResult{oracle: targetOracle, stoodDown: stoodDown}
		if target == pkgPath {
			oracle = targetOracle
		}
	}
	t.derivedMu.Unlock()
	return slices.Clone(oracle), slices.Clone(stoodDown), nil
}

func (t *Tree) rtaIndex() (map[types.Object][]types.Object, map[string][]types.Object) {
	b := t.buildIndex()
	b.rtaOnce.Do(func() {
		adj := make(map[types.Object][]types.Object)
		pkgInits := make(map[string][]types.Object)
		var concreteNamed []*types.Named
		seenNamed := make(map[*types.Named]bool)
		var collectNamed func(typ types.Type, depth int)
		collectNamed = func(typ types.Type, depth int) {
			if typ == nil || depth > 8 {
				return
			}
			switch u := typ.(type) {
			case *types.Named:
				if seenNamed[u] {
					return
				}
				seenNamed[u] = true
				if !types.IsInterface(u.Underlying()) {
					concreteNamed = append(concreteNamed, u)
				}
				collectNamed(u.Underlying(), depth+1)
			case *types.Pointer:
				collectNamed(u.Elem(), depth+1)
			case *types.Slice:
				collectNamed(u.Elem(), depth+1)
			case *types.Array:
				collectNamed(u.Elem(), depth+1)
			case *types.Map:
				collectNamed(u.Key(), depth+1)
				collectNamed(u.Elem(), depth+1)
			case *types.Chan:
				collectNamed(u.Elem(), depth+1)
			case *types.Struct:
				for i := 0; i < u.NumFields(); i++ {
					collectNamed(u.Field(i).Type(), depth+1)
				}
			case *types.Signature:
				if p := u.Params(); p != nil {
					for i := 0; i < p.Len(); i++ {
						collectNamed(p.At(i).Type(), depth+1)
					}
				}
				if r := u.Results(); r != nil {
					for i := 0; i < r.Len(); i++ {
						collectNamed(r.At(i).Type(), depth+1)
					}
				}
			}
		}
		var ifaceMethods []*types.Func
		seenIfaceMethod := make(map[*types.Func]bool)
		canonicalObj := func(obj types.Object) types.Object {
			switch u := obj.(type) {
			case *types.Func:
				return u.Origin()
			case *types.Var:
				return u.Origin()
			default:
				return obj
			}
		}
		addEdge := func(from, to types.Object) {
			from = canonicalObj(from)
			to = canonicalObj(to)
			if from == nil || to == nil || from == to {
				return
			}
			switch u := to.(type) {
			case *types.Func:
				if sig, ok := u.Type().(*types.Signature); ok && sig != nil && sig.Recv() != nil && types.IsInterface(sig.Recv().Type()) {
					if !seenIfaceMethod[u] {
						seenIfaceMethod[u] = true
						ifaceMethods = append(ifaceMethods, u)
					}
				}
			case *types.Var:
			default:
				return
			}
			adj[from] = append(adj[from], to)
		}
		collectNodeUses := func(pkg *packages.Package, node ast.Node, into func(types.Object)) {
			if node == nil || pkg.TypesInfo == nil {
				return
			}
			ast.Inspect(node, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if obj := pkg.TypesInfo.Uses[x]; obj != nil {
						into(obj)
					}
				case *ast.SelectorExpr:
					if sel := pkg.TypesInfo.Selections[x]; sel != nil {
						if obj := sel.Obj(); obj != nil {
							into(obj)
						}
					}
				}
				return true
			})
		}
		for _, pkg := range t.pkgs {
			if pkg.TypesInfo == nil {
				continue
			}
			for _, tv := range pkg.TypesInfo.Types {
				collectNamed(tv.Type, 0)
			}
			for _, obj := range pkg.TypesInfo.Defs {
				if obj != nil {
					collectNamed(obj.Type(), 0)
				}
			}
			for _, obj := range pkg.TypesInfo.Uses {
				if obj != nil {
					collectNamed(obj.Type(), 0)
				}
			}
			bp := basePackagePath(pkg)
			for _, f := range pkg.Syntax {
				for _, decl := range f.Decls {
					switch d := decl.(type) {
					case *ast.FuncDecl:
						fnObj := pkg.TypesInfo.Defs[d.Name]
						if fnObj == nil {
							continue
						}
						if isInitDecl(d) && bp != "" {
							pkgInits[bp] = append(pkgInits[bp], fnObj)
						}
						if d.Body != nil {
							collectNodeUses(pkg, d.Body, func(to types.Object) {
								addEdge(fnObj, to)
							})
						}
					case *ast.GenDecl:
						for _, spec := range d.Specs {
							vs, ok := spec.(*ast.ValueSpec)
							if !ok {
								continue
							}
							var refs []types.Object
							for _, val := range vs.Values {
								collectNodeUses(pkg, val, func(to types.Object) {
									refs = append(refs, to)
								})
							}
							for _, name := range vs.Names {
								varObj := pkg.TypesInfo.Defs[name]
								if varObj == nil {
									continue
								}
								if bp != "" {
									pkgInits[bp] = append(pkgInits[bp], varObj)
								}
								for _, ref := range refs {
									addEdge(varObj, ref)
								}
							}
						}
					}
				}
			}
		}
		for _, m := range ifaceMethods {
			mSig, ok := m.Type().(*types.Signature)
			if !ok || mSig == nil {
				continue
			}
			for _, named := range concreteNamed {
				obj, _, _ := types.LookupFieldOrMethod(named, true, m.Pkg(), m.Name())
				cm, ok := obj.(*types.Func)
				if !ok || cm == nil {
					continue
				}
				cmSig, ok := cm.Type().(*types.Signature)
				if !ok || cmSig == nil {
					continue
				}
				if cmSig.Variadic() == mSig.Variadic() &&
					cmSig.Params().Len() == mSig.Params().Len() &&
					cmSig.Results().Len() == mSig.Results().Len() {
					adj[m] = append(adj[m], cm.Origin())
				}
			}
		}
		b.rtaAdj = adj
		b.rtaPkgInits = pkgInits
	})
	return b.rtaAdj, b.rtaPkgInits
}

func (t *Tree) resolveSymbolObjectsUnlocked(symbol string) (objs []types.Object, pkgPath string, isInit bool) {
	b := t.buildIndex()
	var rest string
	for i := strings.LastIndexByte(symbol, '.'); i > 0; i = strings.LastIndexByte(symbol[:i], '.') {
		if b.basePackages[symbol[:i]] {
			pkgPath, rest = symbol[:i], symbol[i+1:]
			break
		}
	}
	if pkgPath == "" {
		return nil, "", false
	}
	if _, _, ok := parseInitSymbol(rest); ok {
		return nil, pkgPath, true
	}
	parts := strings.Split(rest, ".")
	if len(parts) == 0 || len(parts) > 2 {
		return nil, pkgPath, false
	}
	seen := make(map[types.Object]bool)
	for _, pkg := range b.pkgsBySymPath[pkgPath] {
		if len(pkg.Errors) > 0 || pkg.Types == nil {
			continue
		}
		if obj := lookup(pkg.Types, parts); obj != nil && !seen[obj] {
			seen[obj] = true
			objs = append(objs, obj)
		}
	}
	return objs, pkgPath, false
}

// ReachesSymbol reports whether fromSymbol statically reaches targetSymbol
// in the loaded build's Rapid Type Analysis (RTA) call and reference graph.
// When either symbol cannot be resolved in the loaded type graph or
// targetSymbol is a package init declaration, ReachesSymbol returns true so
// static ordering never misclassifies an unresolvable or init target as
// unreachable (REQ-exec-oracle-run).
func (t *Tree) ReachesSymbol(fromSymbol, targetSymbol string) bool {
	if fromSymbol == "" || targetSymbol == "" {
		return true
	}
	if fromSymbol == targetSymbol {
		return true
	}
	t.loadMu.Lock()
	adj, pkgInits := t.rtaIndex()
	fromObjs, fromPkg, _ := t.resolveSymbolObjectsUnlocked(fromSymbol)
	targetObjs, targetPkg, targetIsInit := t.resolveSymbolObjectsUnlocked(targetSymbol)
	t.loadMu.Unlock()
	if targetIsInit {
		return true
	}
	if len(fromObjs) == 0 || len(targetObjs) == 0 {
		return true
	}
	goal := make(map[types.Object]bool, len(targetObjs))
	for _, obj := range targetObjs {
		goal[obj] = true
	}
	visited := make(map[types.Object]bool, 32)
	queue := make([]types.Object, 0, 32)
	enqueue := func(obj types.Object) {
		if obj != nil && !visited[obj] {
			visited[obj] = true
			queue = append(queue, obj)
		}
	}
	for _, obj := range fromObjs {
		enqueue(obj)
	}
	for _, obj := range pkgInits[fromPkg] {
		enqueue(obj)
	}
	if targetPkg != "" && targetPkg != fromPkg {
		for _, obj := range pkgInits[targetPkg] {
			enqueue(obj)
		}
	}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if goal[curr] {
			return true
		}
		for _, next := range adj[curr] {
			if goal[next] {
				return true
			}
			enqueue(next)
		}
	}
	return false
}
