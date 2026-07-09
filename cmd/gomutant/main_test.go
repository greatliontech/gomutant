package main

import (
	"path/filepath"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// TestFindingsAtAndUpdate pins the CLI's document anchoring (a relative
// findings path anchors at the tree root, matching the MCP face) and the
// locked update round trip it shares with the server.
func TestFindingsAtAndUpdate(t *testing.T) {
	dir := t.TempDir()
	path := findingsAt(dir, defaultFindings)
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("default document not anchored at the tree: %s", path)
	}
	abs := filepath.Join(t.TempDir(), "f.json")
	if findingsAt(dir, abs) != abs {
		t.Fatal("absolute findings path rewritten")
	}
	fresh := []gomutant.Finding{{Symbol: "p.A", BodyHash: "h", OperatorSet: "go/2", Toolchain: "tc", Mutants: 1, Killed: 1}}
	err := gomutant.UpdateDocument(path, func(prior []gomutant.Finding) ([]gomutant.Finding, error) {
		return gomutant.MergeFindings(prior, fresh), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadFindings(path)
	if err != nil || len(got) != 1 || got[0].Symbol != "p.A" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}
