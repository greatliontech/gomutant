package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// LinkedTestPackagesContext resolves a nested workspace member's linked
// dependency set from the tree root — the derivation runs under the
// tree's own environment (GOWORK pinned), so a root-run go list sees
// the same module graph the member-scoped load did — and includes the
// test package itself while omitting the synthesized test main
// (REQ-exec-ephemeral's linkage gate).
func TestLinkedTestPackagesResolvesNestedWorkspaceMember(t *testing.T) {
	tr, err := Load("testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := tr.LinkedTestPackagesContext(context.Background(), "example.com/ws/sub")
	if err != nil {
		t.Fatal(err)
	}
	if linked == nil || !linked["example.com/ws/sub"] {
		t.Fatalf("linked set = %v, want the nested member resolved with itself included", linked)
	}
	if linked["example.com/ws/sub.test"] {
		t.Fatalf("linked set admits the synthesized test main: %v", linked)
	}
}

// The derivation runs under the tree's SELECTED environment: a
// build-tag selection changes which files the test binary compiles, so
// the linked set must follow it — a derivation that dropped the tree
// env would silently judge the unselected build.
func TestLinkedTestPackagesFollowsTagSelection(t *testing.T) {
	plain, err := Load("testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	plainSet, err := plain.LinkedTestPackagesContext(context.Background(), "example.com/ws/sub")
	if err != nil {
		t.Fatal(err)
	}
	if plainSet["example.com/ws/sub/gatedimport"] {
		t.Fatalf("untagged linked set admits the gated import: %v", plainSet)
	}
	gated, err := LoadContextSelection(context.Background(), "testdata/workspacemod", Selection{Tags: []string{"gated"}})
	if err != nil {
		t.Fatal(err)
	}
	gatedSet, err := gated.LinkedTestPackagesContext(context.Background(), "example.com/ws/sub")
	if err != nil {
		t.Fatal(err)
	}
	if gatedSet == nil || !gatedSet["example.com/ws/sub/gatedimport"] {
		t.Fatalf("tag-selected linked set lacks the gated import: %v", gatedSet)
	}
}

// The direct scan is the load-bearing fallback when a closure is
// unresolvable: with a nil linked set cached, a directly-importing
// rapid package still detects — the narrow go-list-fails-where-go-test-
// builds residual keeps its direct-importer coverage
// (REQ-exec-property-oracles).
func TestPropertyRuntimesDirectScanCoversUnresolvableClosure(t *testing.T) {
	tr, err := Load("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	const prop = "example.com/fixture/prop"
	tr.linkedMu.Lock()
	tr.linked = map[string]map[string]bool{prop: nil}
	tr.linkedMu.Unlock()
	runtimes, err := tr.PropertyRuntimesContext(context.Background(), []string{prop})
	if err != nil {
		t.Fatal(err)
	}
	if got := runtimes[prop]; len(got) != 1 || got[0] != "rapid" {
		t.Fatalf("direct-scan fallback runtimes = %v, want rapid via the direct import", got)
	}
}

// The expanded default-oracle derivation (REQ-target-default): genp
// has no tests of its own — its teeth live in the consumer package lib
// whose test binary links it. The package set is asserted exactly so
// an over-broad derivation (every test package admitted) fails here,
// not only downstream.
func TestDerivedOracleSpansLinkedPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list per test package")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	oracle, _, err := tr.DerivedOracleContext(ctx, "example.com/fixture/genp")
	if err != nil {
		t.Fatal(err)
	}
	pkgs := map[string]bool{}
	for _, sym := range oracle {
		pkgs[sym[:strings.LastIndex(sym, ".")]] = true
	}
	// Exactly the packages whose test binaries link genp: lib imports
	// it directly, and the two property fixtures import lib.
	want := map[string]bool{"example.com/fixture/lib": true, "example.com/fixture/gopterprop": true, "example.com/fixture/mixedprop": true}
	if len(pkgs) != len(want) {
		t.Fatalf("genp's derived oracle spans %v, want exactly %v", pkgs, want)
	}
	for p := range want {
		if !pkgs[p] {
			t.Fatalf("genp's derived oracle spans %v, want exactly %v", pkgs, want)
		}
	}
	if !slices.Contains(oracle, "example.com/fixture/lib.TestGenpDelta") {
		t.Fatalf("genp's oracle omits the consumer test that holds its teeth: %v", oracle)
	}
	if !slices.IsSorted(oracle) {
		t.Fatalf("derived oracle unsorted: %v", oracle)
	}
}

// A lagging enumeration refuses the derivation for EVERY package —
// including one with no snapshot tests at all: a test file written
// after the load is exactly the silent coverage cap the freshness
// clause exists to catch, and a snapshot-only candidacy was the
// demonstrated hole. A test-bearing package whose linked closure does
// not build stands down BY NAME rather than silently narrowing
// (REQ-target-default).
func TestDerivedOracleRefusesLaggingEnumeration(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a module and runs go list")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/lagmod\n\ngo 1.26\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":      "package b\n\nimport \"example.com/lagmod/a\"\n\nfunc B() int { return a.A() }\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { if B() != 1 { t.Fatal() } }\n",
		// c imports a package that does not exist, so its test binary's
		// closure cannot RESOLVE (go list itself refuses — a type error
		// alone would still list): the derivation must stand it down
		// by name.
		"c/c.go":      "package c\n\nimport _ \"example.com/lagmod/missing\"\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n",
		// d is a TEST-ONLY package (no non-test files): the tree-wide
		// verification must handle it rather than making it fatal for
		// every derivation (the reviewer's unconstructed state).
		"d/d_test.go": "package d\n\nimport \"testing\"\n\nfunc TestD(t *testing.T) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	fresh, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	oracle, stoodDown, err := fresh.DerivedOracleContext(ctx, "example.com/lagmod/a")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(oracle, "example.com/lagmod/b.TestB") {
		t.Fatalf("consumer test missing from the fresh derivation: %v", oracle)
	}
	if len(stoodDown) != 1 || stoodDown[0] != "example.com/lagmod/c" {
		t.Fatalf("unresolvable closure did not stand down by name: %v", stoodDown)
	}

	// The lag: load a tree, THEN land a's first test file, then derive
	// for the first time — the package had no snapshot tests, so a
	// snapshot-derived candidacy would never look at it, and the
	// derivation must refuse instead of silently capping the oracle.
	stale, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "a_test.go"), []byte("package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stale.DerivedOracleContext(ctx, "example.com/lagmod/b"); err == nil || !strings.Contains(err.Error(), "lags the tree") || !strings.Contains(err.Error(), "TestA") {
		t.Fatalf("lagging derivation = %v, want the refusal naming the on-disk-only test", err)
	}

	// A fresh load derives the new test into a's own oracle.
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := reloaded.DerivedOracleContext(ctx, "example.com/lagmod/a")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(after, "example.com/lagmod/a.TestA") || !slices.Contains(after, "example.com/lagmod/b.TestB") {
		t.Fatalf("fresh derivation = %v, want both packages' tests", after)
	}
}
