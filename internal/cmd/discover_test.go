package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverTargetsResolvesEffectiveOracle(t *testing.T) {
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir})
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
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || !view.Targets[0].OracleExplicit || view.Targets[0].Oracle[0] != "example.com/fixture/lib.TestAdd" || view.Targets[0].Labels[0] != "a" {
		t.Fatalf("explicit discovery = %+v", view)
	}
	if _, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path, changed: "HEAD"}); err == nil {
		t.Fatal("targets and changed accepted together")
	}
}

func TestDiscoverTargetsFiltersEveryProducer(t *testing.T) {
	view, err := discoverTargets(context.Background(), discoverOptions{
		dir: fixtureDir, packages: []string{"example.com/fixture/methods"}, symbols: []string{"example.com/fixture/methods.Counter.*"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 2 || view.Targets[0].Symbol != "example.com/fixture/methods.Counter.Inc" || view.Targets[1].Symbol != "example.com/fixture/methods.Counter.Value" {
		t.Fatalf("filtered discovery = %+v", view.Targets)
	}
	if _, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, symbols: []string{"example.com/fixture/lib.Absent"}}); err == nil {
		t.Fatal("empty filtered discovery succeeded")
	}
}
