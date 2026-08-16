package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The tree cache is selection-keyed: two selections are two loader
// input sets even over byte-identical source, so a cached tree serves
// only calls naming the selection that loaded it, while repeat calls
// under one selection reuse the cache (REQ-target-selection).
func TestTreeCacheKeyedBySelection(t *testing.T) {
	s := serverAt(t)
	gated := filepath.Join(s.dir, "gatedpkg", "gated.go")
	if err := os.MkdirAll(filepath.Dir(gated), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gated, []byte("//go:build seltag\n\npackage gatedpkg\n\nfunc Gated() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain, err := s.loadTreeContext(context.Background(), gomutant.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.loadTreeContext(context.Background(), gomutant.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if plain != again {
		t.Fatal("repeat selection-less call did not serve the cached tree")
	}
	tagged, err := s.loadTreeContext(context.Background(), gomutant.Selection{Tags: []string{"seltag"}})
	if err != nil {
		t.Fatal(err)
	}
	if tagged == plain {
		t.Fatal("a declared selection served the selection-less cached tree")
	}
	// A malformed declaration refuses identically cold or warm: a
	// comma-carrying tag joins to the same key as the cached valid
	// split pair, so without the boundary check the warm cache serves
	// where a cold load refuses.
	if _, err := s.loadTreeContext(context.Background(), gomutant.Selection{Tags: []string{"sel", "tag"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadTreeContext(context.Background(), gomutant.Selection{Tags: []string{"sel,tag"}}); err == nil {
		t.Fatal("a warm cache served a malformed selection")
	}

	targets, err := tagged.DiscoverContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, target := range targets {
		if target.Symbol == "example.com/fixture/gatedpkg.Gated" {
			found = true
		}
	}
	if !found {
		t.Fatal("the selection-keyed tree did not load the tag-gated symbol")
	}
}
