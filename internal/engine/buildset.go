package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// buildSet indexes the loaded build once: package import paths and the
// absolute Go files the build compiles, so ephemeral validation can
// refuse inputs the build would silently ignore
// (REQ-exec-ephemeral's validation arm).
type buildSet struct {
	once     sync.Once
	packages map[string]bool
	files    map[string]bool
	filePkg  map[string]string
}

func (t *Tree) buildIndex() *buildSet {
	t.build.once.Do(func() {
		t.build.packages = make(map[string]bool, len(t.pkgs))
		t.build.files = make(map[string]bool)
		t.build.filePkg = make(map[string]string)
		for _, pkg := range t.pkgs {
			t.build.packages[pkg.PkgPath] = true
			for _, file := range pkg.GoFiles {
				clean := filepath.Clean(file)
				t.build.files[clean] = true
				t.build.filePkg[clean] = pkg.PkgPath
			}
		}
	})
	return &t.build
}

// HasPackage reports whether path names a loaded package import path.
func (t *Tree) HasPackage(path string) bool {
	return t.buildIndex().packages[path]
}

// BuildCompilesFile reports whether the loaded build compiles the
// absolute file: a build-constraint-excluded source or a data file is
// not in any loaded package's GoFiles, so an overlay of it can never be
// exercised.
func (t *Tree) BuildCompilesFile(abs string) bool {
	return t.buildIndex().files[filepath.Clean(abs)]
}

// FileImportPath reports the loaded package import path compiling the
// absolute file, empty when no loaded package does - the coverage
// profile's file keys are import-path-qualified, so classifying a
// replacement against a profile starts here.
func (t *Tree) FileImportPath(abs string) string {
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
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-test", "-f", "{{.ImportPath}}", testPkg)
	cmd.Dir = t.dir
	cmd.Env = t.env
	out, err := cmd.Output()
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
		// A start failure (fork/exec under memory pressure, RLIMIT)
		// says nothing about the closure — standing down here would
		// silently re-open the unlinked-false-survivor channel when a
		// later spawn succeeds, so the derivation failure is the
		// caller's error.
		return nil, fmt.Errorf("resolving %s's linked dependency set: %w", testPkg, err)
	}
	set = map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if i := strings.Index(line, " ["); i >= 0 {
			// A test variant prints bracket-suffixed ("pkg_test
			// [pkg.test]"); its files belong to the plain import path
			// the build index records, so only genuinely bracketed
			// lines are cut. A real tree package that shares the
			// "<pkg>_test" import path would be indistinguishable —
			// the go tool itself claims that path for the synthesized
			// external test package — an inherent collision with no
			// discriminator, accepted.
			set[line[:i]] = true
			continue
		}
		if line == testPkg+".test" {
			// The synthesized test main is no source package; keeping
			// it would wrongly admit a real tree package that happens
			// to carry the ".test"-suffixed import path.
			continue
		}
		set[line] = true
	}
	t.linkedMu.Lock()
	if t.linked == nil {
		t.linked = map[string]map[string]bool{}
	}
	t.linked[testPkg] = set
	t.linkedMu.Unlock()
	return set, nil
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
	for _, pkg := range t.pkgs {
		if base := basePackagePath(pkg); base != "" && !seen[base] {
			seen[base] = true
			bases = append(bases, base)
		}
	}
	sort.Strings(bases)
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
		linked, err := t.LinkedTestPackagesContext(ctx, base)
		if err != nil {
			return nil, nil, err
		}
		if linked == nil {
			stoodDown = append(stoodDown, base)
			continue
		}
		if linked[pkgPath] {
			oracle = append(oracle, tests...)
		}
	}
	sort.Strings(oracle)
	sort.Strings(stoodDown)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	t.derivedMu.Lock()
	if t.derivedOracles == nil {
		t.derivedOracles = map[string]derivedOracleResult{}
	}
	t.derivedOracles[pkgPath] = derivedOracleResult{oracle: oracle, stoodDown: stoodDown}
	t.derivedMu.Unlock()
	return slices.Clone(oracle), slices.Clone(stoodDown), nil
}
