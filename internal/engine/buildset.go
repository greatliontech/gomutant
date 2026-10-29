package engine

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
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
	rtaOnce            sync.Once
	rtaAdj             map[types.Object][]types.Object
	rtaPkgInits        map[string][]types.Object
	rtaReflective      map[types.Object]bool
	rtaCanonByKey      map[string]types.Object
	rtaNodeTypes       map[types.Object][]string
	rtaIfaceImpls      map[*types.Func]map[string][]*types.Func
	rtaAnyByType       map[string][]*types.Func
	rtaAnyProtocolObj  types.Object
	rtaSeenIfaceMethod map[*types.Func]bool
	rtaClosuresMu      sync.Mutex
	rtaClosures        map[[2]string]rtaClosureResult
}

type rtaClosureResult struct {
	visited      map[types.Object]bool
	isReflective bool
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

var anyProtocolMethodNames = []string{
	"Error",
	"Unwrap",
	"Is",
	"As",
	"String",
	"GoString",
	"Format",
	"Scan",
	"MarshalJSON",
	"UnmarshalJSON",
	"MarshalText",
	"UnmarshalText",
	"AppendText",
	"MarshalBinary",
	"UnmarshalBinary",
	"AppendBinary",
	"MarshalXML",
	"UnmarshalXML",
	"MarshalXMLAttr",
	"UnmarshalXMLAttr",
	"GobEncode",
	"GobDecode",
	"Value",
	"LogValue",
}

func rtaObjectKey(obj types.Object) string {
	if obj == nil {
		return ""
	}
	switch u := obj.(type) {
	case *types.Func:
		u = u.Origin()
		if u.Pkg() == nil {
			return ""
		}
		sig, ok := u.Type().(*types.Signature)
		if !ok || sig == nil {
			return ""
		}
		if sig.Recv() != nil {
			rt := types.Unalias(sig.Recv().Type())
			if ptr, ok := rt.(*types.Pointer); ok {
				rt = types.Unalias(ptr.Elem())
			}
			if named, ok := rt.(*types.Named); ok {
				if tn := named.Origin().Obj(); tn != nil && tn.Pkg() != nil {
					return tn.Pkg().Path() + "." + tn.Name() + "." + u.Name()
				}
			}
			return ""
		}
		if u.Name() == "init" && sig.Params().Len() == 0 && sig.Results().Len() == 0 {
			return ""
		}
		return u.Pkg().Path() + "." + u.Name()
	case *types.Var:
		u = u.Origin()
		if u.Pkg() == nil || u.IsField() {
			return ""
		}
		if scope := u.Pkg().Scope(); scope != nil && (u.Parent() == scope || scope.Lookup(u.Name()) == u) {
			return u.Pkg().Path() + "." + u.Name()
		}
		return ""
	default:
		return ""
	}
}

func rtaTypeKey(named *types.Named) string {
	if named == nil {
		return ""
	}
	orig := named.Origin()
	if types.IsInterface(orig.Underlying()) {
		return ""
	}
	tn := orig.Obj()
	if tn == nil || tn.Pkg() == nil {
		return ""
	}
	return tn.Pkg().Path() + "." + tn.Name()
}

func (t *Tree) rtaIndex() (map[types.Object][]types.Object, map[string][]types.Object) {
	b := t.buildIndex()
	b.rtaOnce.Do(func() {
		adj := make(map[types.Object][]types.Object)
		pkgInits := make(map[string][]types.Object)
		reflective := make(map[types.Object]bool)
		canonByKey := make(map[string]types.Object)
		nodeTypes := make(map[types.Object][]string)
		var concreteNamed []*types.Named
		seenNamed := make(map[*types.Named]bool)
		var collectNamed func(typ types.Type, depth int)
		collectNamed = func(typ types.Type, depth int) {
			if typ == nil || depth > 8 {
				return
			}
			switch u := typ.(type) {
			case *types.Alias:
				collectNamed(types.Unalias(u), depth+1)
			case *types.Named:
				if targs := u.TypeArgs(); targs != nil {
					for i := 0; i < targs.Len(); i++ {
						collectNamed(targs.At(i), depth+1)
					}
				}
				orig := u.Origin()
				if seenNamed[orig] {
					return
				}
				seenNamed[orig] = true
				if !types.IsInterface(orig.Underlying()) {
					concreteNamed = append(concreteNamed, orig)
				}
				collectNamed(orig.Underlying(), depth+1)
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
				if u.Recv() != nil {
					collectNamed(u.Recv().Type(), depth+1)
				}
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
		addNodeTypes := func(owner types.Object, rootTyp types.Type, seenKey map[string]bool) {
			if owner == nil || rootTyp == nil {
				return
			}
			seenType := make(map[types.Type]bool)
			var walk func(typ types.Type, depth int)
			walk = func(typ types.Type, depth int) {
				if typ == nil || depth > 6 {
					return
				}
				u := types.Unalias(typ)
				if seenType[u] {
					return
				}
				seenType[u] = true
				switch t := u.(type) {
				case *types.Named:
					if targs := t.TypeArgs(); targs != nil {
						for i := 0; i < targs.Len(); i++ {
							walk(targs.At(i), depth+1)
						}
					}
					orig := t.Origin()
					if tk := rtaTypeKey(orig); tk != "" && !seenKey[tk] {
						seenKey[tk] = true
						nodeTypes[owner] = append(nodeTypes[owner], tk)
					}
					if !types.IsInterface(orig.Underlying()) {
						walk(orig.Underlying(), depth+1)
					}
				case *types.Pointer:
					walk(t.Elem(), depth+1)
				case *types.Slice:
					walk(t.Elem(), depth+1)
				case *types.Array:
					walk(t.Elem(), depth+1)
				case *types.Map:
					walk(t.Key(), depth+1)
					walk(t.Elem(), depth+1)
				case *types.Chan:
					walk(t.Elem(), depth+1)
				case *types.Struct:
					for i := 0; i < t.NumFields(); i++ {
						walk(t.Field(i).Type(), depth+1)
					}
				case *types.Signature:
					if t.Recv() != nil {
						walk(t.Recv().Type(), depth+1)
					}
					if p := t.Params(); p != nil {
						for i := 0; i < p.Len(); i++ {
							walk(p.At(i).Type(), depth+1)
						}
					}
					if r := t.Results(); r != nil {
						for i := 0; i < r.Len(); i++ {
							walk(r.At(i).Type(), depth+1)
						}
					}
				}
			}
			walk(rootTyp, 0)
		}
		var ifaceMethods []*types.Func
		seenIfaceMethod := make(map[*types.Func]bool)
		canonicalObj := func(obj types.Object) types.Object {
			if obj == nil {
				return nil
			}
			switch u := obj.(type) {
			case *types.Func:
				obj = u.Origin()
			case *types.Var:
				obj = u.Origin()
			}
			if key := rtaObjectKey(obj); key != "" {
				if c, ok := canonByKey[key]; ok {
					return c
				}
				canonByKey[key] = obj
			}
			return obj
		}
		registerIfaceMethod := func(m *types.Func) *types.Func {
			m = canonicalObj(m).(*types.Func)
			if !seenIfaceMethod[m] {
				seenIfaceMethod[m] = true
				ifaceMethods = append(ifaceMethods, m)
			}
			return m
		}
		anyProtocolObj := types.NewVar(token.NoPos, nil, "$anyProtocol", nil)
		anyProtocolUsed := false
		pkgAnyNodes := make(map[string]types.Object)
		collectPkgScopeInterfaces := func(pkg *types.Package) types.Object {
			if pkg == nil {
				return nil
			}
			if node, ok := pkgAnyNodes[pkg.Path()]; ok {
				return node
			}
			node := types.NewVar(token.NoPos, nil, "$pkgAny:"+pkg.Path(), nil)
			pkgAnyNodes[pkg.Path()] = node
			scanScope := func(s *types.Scope) {
				if s == nil {
					return
				}
				for _, name := range s.Names() {
					obj := s.Lookup(name)
					tn, ok := obj.(*types.TypeName)
					if !ok || tn == nil {
						continue
					}
					iface, ok := tn.Type().Underlying().(*types.Interface)
					if !ok || iface == nil {
						continue
					}
					for i := 0; i < iface.NumMethods(); i++ {
						m := registerIfaceMethod(iface.Method(i))
						adj[node] = append(adj[node], m)
					}
				}
			}
			scanScope(pkg.Scope())
			for _, imp := range pkg.Imports() {
				scanScope(imp.Scope())
			}
			return node
		}
		seenExtFunc := make(map[*types.Func]bool)
		collectExtInterfaces := func(from types.Object, srcPkg *types.Package, rootTyp types.Type) {
			seenType := make(map[types.Type]bool)
			var walk func(typ types.Type, depth int)
			walk = func(typ types.Type, depth int) {
				if typ == nil || depth > 6 {
					return
				}
				u := types.Unalias(typ)
				if seenType[u] {
					return
				}
				seenType[u] = true
				switch t := u.(type) {
				case *types.Named:
					if targs := t.TypeArgs(); targs != nil {
						for i := 0; i < targs.Len(); i++ {
							walk(targs.At(i), depth+1)
						}
					}
					if tn := t.Obj(); tn != nil && tn.Pkg() == nil && tn.Name() == "error" {
						anyProtocolUsed = true
						adj[from] = append(adj[from], anyProtocolObj)
					}
					if iface, ok := t.Underlying().(*types.Interface); ok {
						for i := 0; i < iface.NumMethods(); i++ {
							m := registerIfaceMethod(iface.Method(i))
							adj[from] = append(adj[from], m)
						}
						return
					}
					walk(t.Underlying(), depth+1)
				case *types.Interface:
					if t.NumMethods() == 0 {
						anyProtocolUsed = true
						adj[from] = append(adj[from], anyProtocolObj)
						if node := collectPkgScopeInterfaces(srcPkg); node != nil {
							adj[from] = append(adj[from], node)
						}
						return
					}
					for i := 0; i < t.NumMethods(); i++ {
						m := registerIfaceMethod(t.Method(i))
						adj[from] = append(adj[from], m)
					}
				case *types.Pointer:
					walk(t.Elem(), depth+1)
				case *types.Slice:
					walk(t.Elem(), depth+1)
				case *types.Array:
					walk(t.Elem(), depth+1)
				case *types.Map:
					walk(t.Key(), depth+1)
					walk(t.Elem(), depth+1)
				case *types.Chan:
					walk(t.Elem(), depth+1)
				case *types.Struct:
					for i := 0; i < t.NumFields(); i++ {
						walk(t.Field(i).Type(), depth+1)
					}
				case *types.Signature:
					if depth == 0 && t.Recv() != nil {
						walk(t.Recv().Type(), depth+1)
					}
					if p := t.Params(); p != nil {
						for i := 0; i < p.Len(); i++ {
							walk(p.At(i).Type(), depth+1)
						}
					}
					if r := t.Results(); r != nil {
						for i := 0; i < r.Len(); i++ {
							walk(r.At(i).Type(), depth+1)
						}
					}
				}
			}
			walk(rootTyp, 0)
		}
		isInTreePkg := func(pkg *types.Package) bool {
			if pkg == nil {
				return false
			}
			bp, _ := strings.CutSuffix(pkg.Path(), "_test")
			return b.basePackages[bp]
		}
		addEdge := func(from, to types.Object) {
			from = canonicalObj(from)
			to = canonicalObj(to)
			if from == nil || to == nil || from == to {
				return
			}
			switch u := to.(type) {
			case *types.Func:
				if sig, ok := u.Type().(*types.Signature); ok && sig != nil {
					if sig.Recv() != nil && types.IsInterface(sig.Recv().Type()) {
						registerIfaceMethod(u)
					} else if !isInTreePkg(u.Pkg()) {
						if !seenExtFunc[u] {
							seenExtFunc[u] = true
							if u.Pkg() != nil && u.Pkg().Path() == "reflect" && (u.Name() == "Call" || u.Name() == "CallSlice" || u.Name() == "MethodByName") {
								reflective[u] = true
							}
							collectExtInterfaces(u, u.Pkg(), sig)
						}
					}
				}
			case *types.Var:
				if !isInTreePkg(u.Pkg()) {
					collectExtInterfaces(from, u.Pkg(), u.Type())
					return
				}
			default:
				return
			}
			adj[from] = append(adj[from], to)
		}
		collectNodeUses := func(pkg *packages.Package, node ast.Node, into func(types.Object), intoType func(types.Type)) {
			if node == nil || pkg.TypesInfo == nil {
				return
			}
			ast.Inspect(node, func(n ast.Node) bool {
				if expr, ok := n.(ast.Expr); ok {
					if tv, ok := pkg.TypesInfo.Types[expr]; ok && tv.Type != nil {
						intoType(tv.Type)
					}
				}
				switch x := n.(type) {
				case *ast.Ident:
					if obj := pkg.TypesInfo.Defs[x]; obj != nil {
						intoType(obj.Type())
					}
					if obj := pkg.TypesInfo.Uses[x]; obj != nil {
						into(obj)
						intoType(obj.Type())
					}
				case *ast.SelectorExpr:
					if pkg.TypesInfo.Uses[x.Sel] == nil {
						if sel := pkg.TypesInfo.Selections[x]; sel != nil {
							if obj := sel.Obj(); obj != nil {
								into(obj)
								intoType(obj.Type())
							}
						}
					}
				}
				return true
			})
		}
		seenNodeKeys := make(map[types.Object]map[string]bool)
		recordTypeForOwner := func(owner types.Object, typ types.Type) {
			if owner == nil || typ == nil {
				return
			}
			sk := seenNodeKeys[owner]
			if sk == nil {
				sk = make(map[string]bool)
				seenNodeKeys[owner] = sk
			}
			addNodeTypes(owner, typ, sk)
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
						fnObj := canonicalObj(pkg.TypesInfo.Defs[d.Name])
						if fnObj == nil {
							continue
						}
						recordTypeForOwner(fnObj, fnObj.Type())
						if isInitDecl(d) && bp != "" {
							pkgInits[bp] = append(pkgInits[bp], fnObj)
						}
						if d.Body != nil {
							collectNodeUses(pkg, d.Body, func(to types.Object) {
								addEdge(fnObj, to)
							}, func(typ types.Type) {
								recordTypeForOwner(fnObj, typ)
							})
						}
					case *ast.GenDecl:
						for _, spec := range d.Specs {
							vs, ok := spec.(*ast.ValueSpec)
							if !ok {
								continue
							}
							var refs []types.Object
							var refTypes []types.Type
							for _, val := range vs.Values {
								collectNodeUses(pkg, val, func(to types.Object) {
									refs = append(refs, to)
								}, func(typ types.Type) {
									refTypes = append(refTypes, typ)
								})
							}
							for _, name := range vs.Names {
								varObj := canonicalObj(pkg.TypesInfo.Defs[name])
								if varObj == nil {
									continue
								}
								recordTypeForOwner(varObj, varObj.Type())
								for _, ref := range refs {
									addEdge(varObj, ref)
								}
								for _, rt := range refTypes {
									recordTypeForOwner(varObj, rt)
								}
								if bp != "" && len(adj[varObj]) > 0 {
									pkgInits[bp] = append(pkgInits[bp], varObj)
								}
							}
						}
					}
				}
			}
		}
		methodsByTypeAndName := make(map[string]map[string][]*types.Func)
		seenConcreteMethod := make(map[string]map[*types.Func]bool)
		addConcreteMethod := func(tk string, cm *types.Func) {
			if tk == "" || cm == nil {
				return
			}
			cm = canonicalObj(cm).(*types.Func)
			if seenConcreteMethod[tk] == nil {
				seenConcreteMethod[tk] = make(map[*types.Func]bool)
				methodsByTypeAndName[tk] = make(map[string][]*types.Func)
			}
			if seenConcreteMethod[tk][cm] {
				return
			}
			seenConcreteMethod[tk][cm] = true
			methodsByTypeAndName[tk][cm.Name()] = append(methodsByTypeAndName[tk][cm.Name()], cm)
		}
		for _, named := range concreteNamed {
			tk := rtaTypeKey(named)
			if tk == "" {
				continue
			}
			for i := 0; i < named.NumMethods(); i++ {
				addConcreteMethod(tk, named.Method(i))
			}
			mset := types.NewMethodSet(types.NewPointer(named))
			for i := 0; i < mset.Len(); i++ {
				if cm, ok := mset.At(i).Obj().(*types.Func); ok {
					addConcreteMethod(tk, cm)
				}
			}
		}
		anyByType := make(map[string][]*types.Func)
		if anyProtocolUsed {
			for tk, byName := range methodsByTypeAndName {
				for _, name := range anyProtocolMethodNames {
					if cms := byName[name]; len(cms) > 0 {
						anyByType[tk] = append(anyByType[tk], cms...)
					}
				}
			}
		}
		ifaceImpls := make(map[*types.Func]map[string][]*types.Func, len(ifaceMethods))
		for _, m := range ifaceMethods {
			mSig, ok := m.Type().(*types.Signature)
			if !ok || mSig == nil {
				continue
			}
			for tk, byName := range methodsByTypeAndName {
				for _, cm := range byName[m.Name()] {
					if !m.Exported() {
						if m.Pkg() == nil || cm.Pkg() == nil || m.Pkg().Path() != cm.Pkg().Path() {
							continue
						}
					}
					cmSig, ok := cm.Type().(*types.Signature)
					if !ok || cmSig == nil {
						continue
					}
					if cmSig.Variadic() == mSig.Variadic() &&
						cmSig.Params().Len() == mSig.Params().Len() &&
						cmSig.Results().Len() == mSig.Results().Len() {
						if ifaceImpls[m] == nil {
							ifaceImpls[m] = make(map[string][]*types.Func)
						}
						ifaceImpls[m][tk] = append(ifaceImpls[m][tk], cm)
					}
				}
			}
		}
		b.rtaAdj = adj
		b.rtaPkgInits = pkgInits
		b.rtaReflective = reflective
		b.rtaCanonByKey = canonByKey
		b.rtaNodeTypes = nodeTypes
		b.rtaIfaceImpls = ifaceImpls
		b.rtaAnyByType = anyByType
		b.rtaAnyProtocolObj = anyProtocolObj
		b.rtaSeenIfaceMethod = seenIfaceMethod
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
	reflective := t.build.rtaReflective
	canonByKey := t.build.rtaCanonByKey
	nodeTypes := t.build.rtaNodeTypes
	ifaceImpls := t.build.rtaIfaceImpls
	anyByType := t.build.rtaAnyByType
	anyProtocolObj := t.build.rtaAnyProtocolObj
	seenIfaceMethod := t.build.rtaSeenIfaceMethod
	fromObjs, fromPkg, _ := t.resolveSymbolObjectsUnlocked(fromSymbol)
	targetObjs, targetPkg, targetIsInit := t.resolveSymbolObjectsUnlocked(targetSymbol)
	t.loadMu.Unlock()
	if targetIsInit {
		return true
	}
	if len(fromObjs) == 0 || len(targetObjs) == 0 {
		return true
	}
	canon := func(obj types.Object) types.Object {
		if key := rtaObjectKey(obj); key != "" {
			if c, ok := canonByKey[key]; ok {
				return c
			}
		}
		return obj
	}
	closureKey := [2]string{fromSymbol, targetPkg}
	t.build.rtaClosuresMu.Lock()
	closure, cached := t.build.rtaClosures[closureKey]
	t.build.rtaClosuresMu.Unlock()
	if !cached {
		visited := make(map[types.Object]bool, 32)
		seenInitPkg := make(map[string]bool, 8)
		activeTypes := make(map[string]bool, 16)
		activeIfaces := make(map[*types.Func]bool, 8)
		anyActive := false
		isReflective := false
		queue := make([]types.Object, 0, 32)
		enqueue := func(obj types.Object) {
			if obj != nil && !visited[obj] {
				visited[obj] = true
				queue = append(queue, obj)
			}
		}
		activateType := func(tk string) {
			if tk == "" || activeTypes[tk] {
				return
			}
			activeTypes[tk] = true
			for m := range activeIfaces {
				for _, cm := range ifaceImpls[m][tk] {
					enqueue(cm)
				}
			}
			if anyActive {
				for _, cm := range anyByType[tk] {
					enqueue(cm)
				}
			}
		}
		activateIface := func(m *types.Func) {
			if m == nil || activeIfaces[m] {
				return
			}
			activeIfaces[m] = true
			for tk := range activeTypes {
				for _, cm := range ifaceImpls[m][tk] {
					enqueue(cm)
				}
			}
		}
		activateAny := func() {
			if anyActive {
				return
			}
			anyActive = true
			for tk := range activeTypes {
				for _, cm := range anyByType[tk] {
					enqueue(cm)
				}
			}
		}
		enqueuePkgInits := func(pkg string) {
			if pkg == "" || seenInitPkg[pkg] {
				return
			}
			seenInitPkg[pkg] = true
			for _, obj := range pkgInits[pkg] {
				enqueue(obj)
			}
		}
		for _, obj := range fromObjs {
			enqueue(canon(obj))
		}
		enqueuePkgInits(fromPkg)
		enqueuePkgInits(targetPkg)
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			if reflective[curr] {
				isReflective = true
			}
			if curr == anyProtocolObj {
				activateAny()
			} else if fn, ok := curr.(*types.Func); ok && seenIfaceMethod[fn] {
				activateIface(fn)
			}
			for _, tk := range nodeTypes[curr] {
				activateType(tk)
			}
			if pkg := curr.Pkg(); pkg != nil {
				bp, _ := strings.CutSuffix(pkg.Path(), "_test")
				enqueuePkgInits(bp)
			}
			for _, next := range adj[curr] {
				if reflective[next] {
					isReflective = true
				}
				enqueue(next)
			}
		}
		closure = rtaClosureResult{visited: visited, isReflective: isReflective}
		t.build.rtaClosuresMu.Lock()
		if t.build.rtaClosures == nil {
			t.build.rtaClosures = make(map[[2]string]rtaClosureResult)
		}
		t.build.rtaClosures[closureKey] = closure
		t.build.rtaClosuresMu.Unlock()
	}
	if closure.isReflective {
		return true
	}
	for _, obj := range targetObjs {
		if closure.visited[canon(obj)] {
			return true
		}
	}
	return false
}

// ScopeDerivedOracle narrows a package-derived oracle to the tests that
// statically reach targetSymbol in the loaded RTA call and reference graph
// (REQ-target-default). When no test in oracle statically reaches
// targetSymbol, it falls back to the target package's own tests in oracle
// (or the full oracle when the target package declares none), so an
// unreached or dynamically dispatched target in a test-bearing package is
// still measured against a suite rather than skipped as oracle-less.
func (t *Tree) ScopeDerivedOracle(pkgPath, targetSymbol string, oracle []string) []string {
	if len(oracle) == 0 || targetSymbol == "" {
		return slices.Clone(oracle)
	}
	var reached []string
	var samePkg []string
	prefix := pkgPath + "."
	for _, testSym := range oracle {
		if strings.HasPrefix(testSym, prefix) && !strings.Contains(testSym[len(prefix):], "/") {
			samePkg = append(samePkg, testSym)
		}
		if t.ReachesSymbol(testSym, targetSymbol) {
			reached = append(reached, testSym)
		}
	}
	if len(reached) > 0 {
		return reached
	}
	if len(samePkg) > 0 {
		return samePkg
	}
	return slices.Clone(oracle)
}
