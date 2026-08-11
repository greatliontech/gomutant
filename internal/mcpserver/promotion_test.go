package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The run response counts records promoted from the machine-local
// overlay into the committed findings document - a document change git
// only sees when committed (REQ-mcp-findings-doc). The dirty measure
// reports none; the clean serve that promotes reports it.
func TestToolRunReportsPromotedRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	s := serverAt(t)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")
	docFile := filepath.Join(s.dir, "lib", "doc.go")
	original, err := os.ReadFile(docFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docFile, append(original, []byte("\n// uncommitted edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	in := runIn{
		TargetsJSON:      `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`,
		Budget:           1,
		OracleTimeoutSec: 120,
	}
	_, dirty, err := s.toolRun(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Promoted != 0 {
		t.Fatalf("dirty measure claimed %d promotions", dirty.Promoted)
	}

	runGit("add", "-A")
	runGit("commit", "-q", "-m", "content lands")

	_, clean, err := s.toolRun(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Promoted != 1 {
		t.Fatalf("clean serve promoted = %d, want 1", clean.Promoted)
	}

	// Attesting against a record that can no longer serve as it stands
	// warns at disposition time (REQ-attest-survivor's echo clause).
	// Editing the oracle test's own body moves the recorded oracle
	// closure - a genuine pin move, not the growth carve-out.
	libTest := filepath.Join(s.dir, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(src), "t.Fatal(\"small arm\")", "t.Fatal(\"small arm moved\")", 1)
	if moved == string(src) {
		t.Fatal("TestWeak edit anchor missing")
	}
	if err := os.WriteFile(libTest, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.loadFindings("")
	if err != nil || len(all) != 1 || len(all[0].Open()) == 0 {
		t.Fatalf("promoted document = %+v, %v", all, err)
	}
	open := all[0].Open()[0]
	_, echo, err := s.toolAttest(ctx, nil, attestIn{
		Symbol: all[0].Symbol, Position: open.Position, Operator: open.Operator, Reason: "equivalent by inspection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if echo.Layer == "" || !strings.Contains(echo.Warning, "stale") {
		t.Fatalf("birth-stale attest echo = %+v", echo)
	}

	// The re-measure judges the birth-stale disposition afresh: a
	// different oracle timeout moves a measurement pin without touching
	// source geometry, so the survivor re-appears at its exact site and
	// only the run-start snapshot can shed the disposition - loudly,
	// with the response rows agreeing with the document
	// (REQ-attest-survivor, REQ-mcp-findings-doc).
	reIn := in
	reIn.OracleTimeoutSec = 180
	_, reMeasured, err := s.toolRun(ctx, nil, reIn)
	if err != nil {
		t.Fatal(err)
	}
	shedSeen := false
	for _, shed := range reMeasured.AttestationSheds {
		if strings.Contains(shed, "pins moved") {
			shedSeen = true
		}
	}
	if !shedSeen {
		t.Fatalf("re-measure did not shed the birth-stale disposition: %+v", reMeasured.AttestationSheds)
	}
	if len(reMeasured.Findings) != 1 || reMeasured.Findings[0].Attested != 0 {
		t.Fatalf("re-measure response still counts the disposition: %+v", reMeasured.Findings)
	}
	final, err := s.loadFindings("")
	if err != nil || len(final) != 1 || len(final[0].Attested) != 0 {
		t.Fatalf("document kept the shed disposition: %+v, %v", final, err)
	}
}
