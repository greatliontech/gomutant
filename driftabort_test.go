package gomutant

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tree edit racing the run refuses target-locally: the drifted
// target is refused with the drift named, the unaffected target's
// completed finding is kept and committed, and the run errors so a
// pipeline never reads the partial campaign as success
// (REQ-exec-quiescence).
func TestRunDriftRefusesTargetLocallyAndKeepsCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
	}
	var committed []string
	libPath := filepath.Join(dir, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(src), "return a + b", "return b + a + 0", 1)
	if moved == string(src) {
		t.Fatal("fixture body not found")
	}
	findings, err := tr.Run(context.Background(), targets, Options{
		Budget: 1,
		Commit: func(f Finding) error { committed = append(committed, f.Symbol); return nil },
		afterExecution: func() {
			if err := os.WriteFile(libPath, []byte(moved), 0o644); err != nil {
				t.Error(err)
			}
		},
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("drifted run error = %v, want a TreeDriftError", err)
	}
	if len(drift.Drifted) == 0 || drift.Drifted[0].Symbol != "example.com/fixture/lib.Add" || drift.Drifted[0].Reason == "" {
		t.Fatalf("drift attribution = %+v", drift.Drifted)
	}
	// The refusal names the moved file itself (gofresh v0.31.0's
	// validation naming arm), not just the subject and class.
	if !strings.Contains(drift.Drifted[0].Reason, "moved: ") || !strings.Contains(drift.Drifted[0].Reason, "lib.go") {
		t.Fatalf("drift reason does not name the moved file: %q", drift.Drifted[0].Reason)
	}
	if drift.Completed != 1 || len(findings) != 1 || findings[0].Symbol != "example.com/fixture/plain.Ok" {
		t.Fatalf("completed retention = %d, findings %+v", drift.Completed, findings)
	}
	for _, symbol := range committed {
		if symbol == "example.com/fixture/lib.Add" {
			t.Fatal("a drift-refused target was committed")
		}
	}
	if !strings.Contains(drift.Error(), "tree changed under measurement") ||
		!strings.Contains(drift.Error(), "re-run to measure the refused set") {
		t.Fatalf("drift message = %q", drift.Error())
	}
}

// The splice path refuses target-locally too: a warmed record whose
// candidate-local evidence re-executes hits the serve-arm validation,
// and a racing edit refuses that target instead of aborting the
// campaign (REQ-exec-quiescence).
func TestRunDriftRefusesSplicedServeTargetLocally(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/candlocal.Value", Oracle: []string{"example.com/fixture/candlocal.TestValue"}}
	first, err := tr.Run(context.Background(), []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(first[0].CandidateEvidence) == 0 {
		t.Fatalf("warming run = %+v, want candidate-local evidence", first)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "candlocal", "candlocal.go")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	var committed []string
	var decisions []RunDecision
	fresh, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := fresh.Run(context.Background(), []Target{target}, Options{
		Prior:    prior,
		Commit:   func(f Finding) error { committed = append(committed, f.Symbol); return nil },
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
		afterExecution: func() {
			if err := os.WriteFile(srcPath, append([]byte(src), []byte("\n// drifted\nfunc Drifted() int { return 9 }\n")...), 0o644); err != nil {
				t.Error(err)
			}
		},
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("drifted splice run error = %v, want a TreeDriftError", err)
	}
	if len(drift.Drifted) != 1 || drift.Drifted[0].Symbol != target.Symbol || drift.Drifted[0].Reason == "" {
		t.Fatalf("splice drift attribution = %+v", drift.Drifted)
	}
	if len(findings) != 0 || len(committed) != 0 {
		t.Fatalf("drift-refused splice retained findings %+v, committed %v", findings, committed)
	}
	// An execution-phase refusal streams no second decision row: the
	// candidate re-execution's cached decision already streamed, and
	// the once-per-target discipline holds (REQ-exec-run-status).
	if len(decisions) != 1 || decisions[0].Action != "cached" {
		t.Fatalf("decision stream = %+v, want the single cached row", decisions)
	}
}

// driftGit initializes and drives a git repository over a fixture copy —
// the shared harness of the ref-motion regressions below.
func driftGit(t *testing.T, dir string) func(args ...string) string {
	t.Helper()
	return func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

// A mid-run ref-only commit — new content in a file no target's producer
// set names, tree bytes identical for every measured package — discards
// nothing: the run succeeds, every finding persists, and each stamp
// carries the commit current at its stamp time, the moved ref included
// (REQ-exec-quiescence, REQ-exec-cancellation). This is the motivating
// defect's regression: the pre-fix engine aborted the whole campaign
// here and discarded completed evidence.
func TestRunPersistsAcrossMidRunRefOnlyCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	git := driftGit(t, dir)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "fixture")
	first := git("rev-parse", "HEAD")
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
	}
	var committed []Finding
	findings, err := tr.Run(context.Background(), targets, Options{
		Budget: 1,
		Commit: func(f Finding) error { committed = append(committed, f); return nil },
		afterExecution: func() {
			if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("ref-only motion\n"), 0o644); err != nil {
				t.Error(err)
				return
			}
			git("add", "unrelated.txt")
			git("commit", "-q", "-m", "mid-run ref motion")
		},
	})
	if err != nil {
		t.Fatalf("ref-only motion aborted the run: %v", err)
	}
	second := git("rev-parse", "HEAD")
	if second == first {
		t.Fatal("test setup produced no ref motion")
	}
	if len(findings) != 2 || len(committed) != 2 {
		t.Fatalf("findings %d, committed %d, want both targets persisted", len(findings), len(committed))
	}
	for _, f := range findings {
		if f.Dirty {
			t.Fatalf("%s stamped dirty across a ref-only move", f.Symbol)
		}
		if f.Commit != second {
			t.Fatalf("%s stamped commit %q, want the stamp-time HEAD %q", f.Symbol, f.Commit, second)
		}
	}
}

// A serve whose freshness proof ran against the run-start view capture
// re-observes its modules from disk once the ref moves past that
// capture: a mid-run commit that changes a served target's content
// refuses exactly that target, target-locally, while the already-served
// sibling stands committed (REQ-exec-quiescence's serve-path arm).
func TestRunServeRefusesContentMovePastViewCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	git := driftGit(t, dir)
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "fixture")
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
	}
	warm, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	warmed, err := warm.Run(context.Background(), targets, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Export(warmed)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(dir, "plain", "plain.go")
	src, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var committed []string
	var decisions []RunDecision
	movedOnce := false
	findings, err := fresh.Run(context.Background(), targets, Options{
		Budget: 1,
		Prior:  prior,
		Commit: func(f Finding) error { committed = append(committed, f.Symbol); return nil },
		Decision: func(d RunDecision) {
			decisions = append(decisions, d)
			// The first target's serve is done (commit precedes the
			// decision); move the SECOND target's content and the ref
			// before its serve begins.
			if movedOnce || d.Symbol != "example.com/fixture/lib.Add" {
				return
			}
			movedOnce = true
			if err := os.WriteFile(plainPath, append([]byte(nil), append(src, []byte("\n// moved past the capture\n")...)...), 0o644); err != nil {
				t.Error(err)
				return
			}
			git("add", "plain/plain.go")
			git("commit", "-q", "-m", "content move past the view capture")
		},
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("content move past the capture = %v, want a TreeDriftError", err)
	}
	if len(drift.Drifted) != 1 || drift.Drifted[0].Symbol != "example.com/fixture/plain.Ok" ||
		!strings.Contains(drift.Drifted[0].Reason, "moved past the run-start view capture") {
		t.Fatalf("drift attribution = %+v", drift.Drifted)
	}
	if len(findings) != 1 || findings[0].Symbol != "example.com/fixture/lib.Add" || !findings[0].Cached {
		t.Fatalf("completed retention = %+v, want the already-served sibling alone", findings)
	}
	for _, symbol := range committed {
		if symbol == "example.com/fixture/plain.Ok" {
			t.Fatal("a capture-stale serve was committed")
		}
	}
	// The refusal's decision row: exactly one for the refused target,
	// action "skipped" — the decision stream's only no-measurement
	// vocabulary (REQ-exec-run-status) — carrying the refusal reason.
	var refusedRows []RunDecision
	for _, d := range decisions {
		if d.Action == "refused" {
			t.Fatalf("decision stream used the outlawed action: %+v", d)
		}
		if d.Symbol == "example.com/fixture/plain.Ok" {
			refusedRows = append(refusedRows, d)
		}
	}
	if len(refusedRows) != 1 || refusedRows[0].Action != "skipped" ||
		!strings.Contains(refusedRows[0].Reason, "moved past the run-start view capture") {
		t.Fatalf("refused target's decision rows = %+v, want one skipped row naming the refusal", refusedRows)
	}
}

// A drift whose residue is an untracked file written under measurement
// names the provenance on the decision line: the operator reads that a
// mutant or oracle process can write into the tree, not generic drift
// that looks like operator error (REQ-exec-quiescence's residue
// sentence).
func TestRunDriftNamesMeasurementResidue(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "fixture")
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}
	libPath := filepath.Join(dir, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(src), "return a + b", "return b + a + 0", 1)
	if moved == string(src) {
		t.Fatal("fixture body not found")
	}
	residue := filepath.Join(dir, "lib", "residue.db")
	ownDoc := filepath.Join(dir, "own-findings.json")
	_, err = tr.Run(context.Background(), targets, Options{
		Budget:    1,
		OwnWrites: RunOwnWrites(ownDoc),
		afterExecution: func() {
			// The tracked edit is the proven drift trigger; the
			// untracked file is the residue the decision line must
			// name alongside it — while the caller's own declared
			// write (its findings document) must not be.
			if err := os.WriteFile(libPath, []byte(moved), 0o644); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(residue, []byte("mutant wrote this"), 0o644); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(ownDoc, []byte("{}"), 0o644); err != nil {
				t.Error(err)
			}
		},
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("residue run error = %v, want a TreeDriftError", err)
	}
	if len(drift.Drifted) == 0 {
		t.Fatalf("drift attribution = %+v", drift)
	}
	reason := drift.Drifted[0].Reason
	if !strings.Contains(reason, "residue.db") || !strings.Contains(reason, "can write into the tree") {
		t.Fatalf("drift reason lacks the measurement-residue provenance: %q", reason)
	}
	if strings.Contains(reason, "own-findings.json") {
		t.Fatalf("the run's own declared write attributed as residue: %q", reason)
	}
}
