package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
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

// The cumulative progress line banks each target as its commit
// returns: the ledger's committed hook feeds the reporter, so the
// structured progress event's committed count reaches the run's
// committed targets (REQ-exec-run-status's progress line;
// REQ-exec-cancellation's claims-only-committed clause).
func TestRunProgressBanksCommittedTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	fixture := isolatedFixture(t)
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts := runOptions{dir: fixture, targetsFile: targetsPath, findingsFile: defaultFindings, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute, jsonl: true, progressEvery: time.Millisecond, output: &out}
	if err := runCommand(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	banked, local := -1, -1
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, `"event":"progress"`) {
			continue
		}
		var event struct {
			TargetsDone  *int `json:"targetsDone"`
			TargetsLocal *int `json:"targetsLocal"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("progress line %q: %v", line, err)
		}
		if event.TargetsDone != nil && *event.TargetsDone > banked {
			if event.TargetsLocal == nil {
				t.Fatalf("progress line %q carries no targetsLocal", line)
			}
			banked, local = *event.TargetsDone, *event.TargetsLocal
		}
	}
	if banked != 1 {
		t.Fatalf("progress banked %d committed targets, want the run's 1:\n%s", banked, out.String())
	}
	// The machine-local count is the store's own classification of the
	// committed record — the layer the write routed it to — not a
	// count the face keeps apart from it.
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(fixture, defaultFindings), fixture)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Load(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("records after the run: %v, %v", records, err)
	}
	wantLocal := 0
	if layer, _ := store.Layer(records[0]); layer == gomutant.LayerLocal {
		wantLocal = 1
	}
	if local != wantLocal {
		t.Fatalf("progress banked %d machine-local, the store classifies the record %d:\n%s", local, wantLocal, out.String())
	}
}
