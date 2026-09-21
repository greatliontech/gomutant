package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// TestTreeStateKeySnapshotRidesTheRunner pins the served face's
// environment snapshot to the tree's runner
// (REQ-exec-go-command-runner).
func TestTreeStateKeySnapshotRidesTheRunner(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/keyed\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var seen []string
	restore := engine.ObserveGoCommandsForTest(func(cmd *exec.Cmd) {
		seen = append(seen, cmd.Dir+" "+cmd.Args[1]+" "+cmd.Args[2])
	})
	defer restore()
	if _, err := treeStateKeyContext(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	for _, spawn := range seen {
		if spawn == dir+" env -json" {
			return
		}
	}
	t.Fatalf("the snapshot did not ride the runner; saw %v", seen)
}
