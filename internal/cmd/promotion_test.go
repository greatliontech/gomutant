package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run that carries a record from the machine-local overlay into the
// committed findings document says so: the document changed in a way
// git only sees when committed (REQ-mcp-findings-doc). The dirty
// measure warns nothing; the clean serve that promotes does.
func TestRunCommandReportsPromotedRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	fixture := isolatedFixture(t)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = fixture
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// isolatedFixture is already a committed git repo; the uncommitted
	// edit below makes the tree dirty for the first measure.
	docFile := filepath.Join(fixture, "lib", "doc.go")
	original, err := os.ReadFile(docFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docFile, append(original, []byte("\n// uncommitted edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := runOptions{dir: fixture, targetsFile: targetsPath, findingsFile: defaultFindings, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute}

	var dirty bytes.Buffer
	opts.output = &dirty
	if err := runCommand(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dirty.String(), "promoted") {
		t.Fatalf("dirty measure claimed a promotion:\n%s", dirty.String())
	}

	runGit("add", "-A")
	runGit("commit", "-q", "-m", "content lands")

	var clean bytes.Buffer
	opts.output = &clean
	if err := runCommand(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clean.String(), "1 record(s) promoted - findings document changed, commit it") {
		t.Fatalf("clean serve did not report the promotion:\n%s", clean.String())
	}
}
