package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverTargetsResolvesEffectiveOracle(t *testing.T) {
	view, err := discoverTargets(discoverOptions{dir: fixtureDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range view.Targets {
		if target.Symbol == "example.com/fixture/lib.Add" {
			if target.OracleExplicit || len(target.Oracle) == 0 {
				t.Fatalf("Add description = %+v", target)
			}
			return
		}
	}
	t.Fatal("Add target not discovered")
}

func TestDiscoverTargetsLoadsExplicitDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "targets.json")
	data := []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/lib.TestWeak","example.com/fixture/lib.TestAdd"],"labels":["z","a"]}]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := discoverTargets(discoverOptions{dir: fixtureDir, targetsFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || !view.Targets[0].OracleExplicit || view.Targets[0].Oracle[0] != "example.com/fixture/lib.TestAdd" || view.Targets[0].Labels[0] != "a" {
		t.Fatalf("explicit discovery = %+v", view)
	}
	if _, err := discoverTargets(discoverOptions{dir: fixtureDir, targetsFile: path, changed: "HEAD"}); err == nil {
		t.Fatal("targets and changed accepted together")
	}
}
