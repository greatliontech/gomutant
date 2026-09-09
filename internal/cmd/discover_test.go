package cmd

import (
	"context"
	"github.com/greatliontech/gomutant/internal/gitfixture"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDiscoverTargetsResolvesEffectiveOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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

func TestDiscoverTargetsLoadsExplicitEmptyOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	path := filepath.Join(t.TempDir(), "targets.json")
	data := []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":[],"labels":["REQ-empty"],"oracleExplicit":true}]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || !view.Targets[0].OracleExplicit || len(view.Targets[0].Oracle) != 0 ||
		view.Targets[0].Skipped != "no oracle" || view.Targets[0].Labels[0] != "REQ-empty" {
		t.Fatalf("explicit-empty discovery = %+v", view)
	}
}

func TestDiscoverTargetsFiltersEveryProducer(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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

// The discover verb's target-source preamble names only what the call
// gave: --changed alone is one source and passes the exclusivity
// check (REQ-exec-preparation).
func TestDiscoverTargetsChangedAloneIsOneSource(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := gitfixture.Changed(t)
	view, err := discoverTargets(context.Background(), discoverOptions{dir: dir, changed: "HEAD"})
	if err != nil {
		t.Fatalf("discover --changed alone refused: %v", err)
	}
	if len(view.Targets) != 1 || view.Targets[0].Symbol != "example.com/dl.Value" {
		t.Fatalf("changed discovery = %+v, want the edited symbol", view.Targets)
	}
}
