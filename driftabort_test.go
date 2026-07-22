package gomutant

import (
	"context"
	"errors"
	"os"
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
	fresh, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := fresh.Run(context.Background(), []Target{target}, Options{
		Prior:  prior,
		Commit: func(f Finding) error { committed = append(committed, f.Symbol); return nil },
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
}
