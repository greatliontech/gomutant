package engine

import (
	"slices"
	"strings"
	"testing"
)

// The selection's environment rewrite: declared tags replace any
// ambient GOFLAGS -tags (never a silent union), the toolchain directive
// replaces GOTOOLCHAIN, other entries pass through untouched, the zero
// selection changes nothing, and malformed declarations refuse before
// any load (REQ-target-selection).
func TestSelectionRewritesFrozenEnvironment(t *testing.T) {
	base := []string{"HOME=/h", "GOFLAGS=-mod=mod -tags=old,stale -trimpath", "GOTOOLCHAIN=local", "PATH=/bin"}

	zero, err := Selection{}.applyEnv(base)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(zero, base) {
		t.Fatalf("zero selection rewrote the environment: %v", zero)
	}

	sel := Selection{Tags: []string{"dst", "extra"}, Toolchain: "go1.26.5"}
	env, err := sel.applyEnv(base)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		byName[name] = value
	}
	if byName["GOTOOLCHAIN"] != "go1.26.5" {
		t.Fatalf("GOTOOLCHAIN = %q, want the declared directive", byName["GOTOOLCHAIN"])
	}
	if got := byName["GOFLAGS"]; !strings.Contains(got, "-tags=dst,extra") || strings.Contains(got, "old,stale") {
		t.Fatalf("GOFLAGS = %q, want declared tags replacing the ambient set", got)
	}
	if got := byName["GOFLAGS"]; !strings.Contains(got, "-mod=mod") || !strings.Contains(got, "-trimpath") {
		t.Fatalf("GOFLAGS = %q, want unrelated ambient flags preserved", got)
	}
	if byName["HOME"] != "/h" || byName["PATH"] != "/bin" {
		t.Fatalf("unrelated entries moved: %v", env)
	}

	// The declared set is order-canonicalized: order and duplicates are
	// presentation, never a distinct selection.
	dup, err := Selection{Tags: []string{"b", "a", "a"}}.applyEnv([]string{"HOME=/h"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dup, "GOFLAGS=-tags=a,b") {
		t.Fatalf("tags were not order-canonicalized: %v", dup)
	}

	// Tags land even when the ambient environment has no GOFLAGS at all.
	env, err = Selection{Tags: []string{"one"}}.applyEnv([]string{"HOME=/h"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "GOFLAGS=-tags=one") {
		t.Fatalf("no-GOFLAGS environment did not gain the declared tags: %v", env)
	}

	for _, bad := range []Selection{
		{Tags: []string{"!negated"}},
		{Tags: []string{"has space"}},
		{Tags: []string{""}},
		{Tags: []string{"a,b"}},
		{Toolchain: "go1.26.5 -x"},
	} {
		if _, err := bad.applyEnv(base); err == nil {
			t.Fatalf("malformed selection %+v did not refuse", bad)
		}
	}
}
