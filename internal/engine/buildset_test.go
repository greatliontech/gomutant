package engine

import (
	"context"
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
