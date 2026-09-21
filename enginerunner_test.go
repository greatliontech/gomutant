package gomutant

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// TestAnalysisEnginesRideTheTreesRunner pins the runner's installation
// on every analysis engine the tree builds
// (REQ-exec-go-command-runner): constructing a subject engine over a
// module spawns the engine's own go commands — its environment
// snapshot — in that module's directory through the tree's runner.
func TestAnalysisEnginesRideTheTreesRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tr := fixtureTree(t)
	moduleDir, _, err := tr.eng.PackageContextContext(context.Background(), "example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	restore := engine.ObserveGoCommandsForTest(func(cmd *exec.Cmd) {
		dir, _ := filepath.Abs(cmd.Dir)
		seen = append(seen, dir+" "+cmd.Args[1])
	})
	defer restore()
	if _, err := tr.newSubjectEngines(nil, false, 0, 0).engineFor(moduleDir); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(moduleDir)
	for _, spawn := range seen {
		if spawn == want+" env" {
			return
		}
	}
	t.Fatalf("no engine go command rode the runner in %s; saw %v", moduleDir, seen)
}
