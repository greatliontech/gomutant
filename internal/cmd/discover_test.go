package cmd

import (
	"context"
	"encoding/json"
	"github.com/greatliontech/gomutant/internal/gitfixture"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverTargetsResolvesEffectiveOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir}, nil)
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
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || !view.Targets[0].OracleExplicit || view.Targets[0].Oracle[0] != "example.com/fixture/lib.TestAdd" || view.Targets[0].Labels[0] != "a" {
		t.Fatalf("explicit discovery = %+v", view)
	}
	if _, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path, changed: "HEAD"}, nil); err == nil {
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
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, targetsFile: path}, nil)
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
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 2 || view.Targets[0].Symbol != "example.com/fixture/methods.Counter.Inc" || view.Targets[1].Symbol != "example.com/fixture/methods.Counter.Value" {
		t.Fatalf("filtered discovery = %+v", view.Targets)
	}
	if _, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, symbols: []string{"example.com/fixture/lib.Absent"}}, nil); err == nil {
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
	view, err := discoverTargets(context.Background(), discoverOptions{dir: dir, changed: "HEAD"}, nil)
	if err != nil {
		t.Fatalf("discover --changed alone refused: %v", err)
	}
	if len(view.Targets) != 1 || view.Targets[0].Symbol != "example.com/dl.Value" {
		t.Fatalf("changed discovery = %+v, want the edited symbol", view.Targets)
	}
}

// The JSON face's residue is a list on every producer: a whole-tree
// discovery renders an empty list, never null.
func TestDiscoverJSONResidueIsAList(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a synthetic tree")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":      "module example.com/res\n\ngo 1.26.4\n",
		"res.go":      "package res\n\nfunc Value() int { return 1 }\n",
		"res_test.go": "package res\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	view, err := discoverTargets(context.Background(), discoverOptions{dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"residue":[]`) {
		t.Fatalf("whole-tree discovery JSON = %s, want an empty residue list", data)
	}
}

// A changed-ref discovery carries the surface's residue — the changed
// paths that produced no target — to the face (REQ-target-changed).
func TestDiscoverChangedCarriesTheResidue(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	fixture := isolatedFixture(t)
	libTest := filepath.Join(fixture, "lib", "lib_test.go")
	original, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libTest, append(original, []byte("\n// an uncommitted test edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := discoverTargets(context.Background(), discoverOptions{dir: fixture, changed: "HEAD"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range view.Residue {
		found = found || r.Path == "lib/lib_test.go"
	}
	if !found {
		t.Fatalf("changed discovery residue = %+v, want the edited test file", view.Residue)
	}
}

// discover refuses its inputs in the one preparation order every verb
// keeps: the build selection's shape before the tree root and the
// changed ref's surface, so a malformed tag is named before git is
// asked about a ref (REQ-exec-preparation).
func TestDiscoverRefusesTheSelectionBeforeTheSurface(t *testing.T) {
	_, err := discoverTargets(context.Background(), discoverOptions{dir: fixtureDir, changed: "nosuchref", tags: []string{"a,b"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "constraint tag") || strings.Contains(err.Error(), "nosuchref") {
		t.Fatalf("discover with a malformed tag and a bad ref = %v; want the tag refused before the surface is read", err)
	}
}
