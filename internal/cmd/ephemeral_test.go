package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

const fixtureDir = "../engine/testdata/fixturemod"

func TestEphemeralBatchOptions(t *testing.T) {
	if err := ephemeralCommand(context.Background(), ephemeralOptions{batch: "batch.json", file: "x.go", testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "omit --file") {
		t.Fatalf("batch with file accepted: %v", err)
	}
	if err := ephemeralCommand(context.Background(), ephemeralOptions{testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("missing mutation form accepted: %v", err)
	}
	if err := ephemeralCommand(context.Background(), ephemeralOptions{dir: "missing", replacement: "r.go", testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "needs --file") {
		t.Fatalf("replacement without file reached tree loading: %v", err)
	}
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(`{"edits":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ephemeralCommand(context.Background(), ephemeralOptions{dir: t.TempDir(), batch: path, testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "edit batch is empty") {
		t.Fatalf("empty batch accepted: %v", err)
	}
}

func TestEphemeralCommandCancellationStopsBeforeInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ephemeralCommand(ctx, ephemeralOptions{batch: "-", testPkg: "p", runPat: "T"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stdin batch = %v", err)
	}
	if err := ephemeralCommand(ctx, ephemeralOptions{replacement: "missing", file: "x.go", testPkg: "p", runPat: "T"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled replacement read = %v", err)
	}
}

func TestReadInputContextCancelsBlockedStdin(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := readInputContext(ctx, "-")
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked stdin cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked stdin reader did not stop")
	}
}

func TestEphemeralCommandTimeoutIncludesInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	original := os.Stdin
	os.Stdin = reader
	defer func() { os.Stdin = original }()

	err = ephemeralCommand(context.Background(), ephemeralOptions{
		batch: "-", testPkg: "p", runPat: "T", timeout: 10 * time.Millisecond, oracleTimeout: time.Hour,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("command timeout during stdin = %v, want context.DeadlineExceeded", err)
	}
}

func TestEphemeralBatchCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	doc := struct {
		Edits []map[string]string `json:"edits"`
	}{Edits: []map[string]string{
		{"file": "lib/lib.go", "old_string": "return a + b", "new_string": "return a + b + manualDelta()"},
		{"file": "lib/doc.go", "old_string": "package lib", "new_string": "package lib\n\nfunc manualDelta() int { return 1 }"},
	}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = ephemeralCommand(context.Background(), ephemeralOptions{dir: fixtureDir, batch: path, testPkg: "example.com/fixture/lib", runPat: "^TestAdd$"})
	if err != nil {
		t.Fatal(err)
	}
}

// The CLI face of REQ-exec-ephemeral's unexercised label: a non-kill
// verdict names every replacement file the probed baseline never
// reaches, so "did not notice" never affirms a false survivor reading.
func TestRenderEphemeralVerdictNamesUnexercisedFiles(t *testing.T) {
	var out bytes.Buffer
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"outside/outside.go"}, Run: "^TestOK$", Runs: 1,
		UnexercisedFiles: []string{"outside/outside.go"},
	})
	text := out.String()
	if !strings.Contains(text, "SURVIVED") || !strings.Contains(text, "unexercised  outside/outside.go") {
		t.Fatalf("survivor render missing the unexercised label:\n%s", text)
	}
	out.Reset()
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"a.go"}, Run: "^TestOK$", Runs: 3, KilledRuns: 1, Killer: "TestOK",
		UnexercisedFiles: []string{"a.go"},
	})
	if text := out.String(); !strings.Contains(text, "FLAKY") || !strings.Contains(text, "unexercised  a.go") {
		t.Fatalf("flaky render missing the unexercised label:\n%s", text)
	}
	// The pruned imports, a mutated test file, and an unknown exercise
	// state each render on their own line: what the probe did to the
	// mutant, and what it could not establish, are part of the verdict.
	out.Reset()
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"a.go", "a_test.go"}, Run: "^TestOK$", Runs: 1,
		PrunedImports: []string{"fmt (a.go)"}, MutatedTests: []string{"a_test.go"}, CoverageUnknown: true, CoverageUnknownFiles: []string{"a.go"},
	})
	if text := out.String(); !strings.Contains(text, "imports pruned  fmt (a.go)") || !strings.Contains(text, "mutated test  a_test.go") || !strings.Contains(text, "coverage unknown  a.go  ") || strings.Contains(text, "coverage unknown  a.go, a_test.go") {
		t.Fatalf("survivor render missing the pruned-imports, mutated-test, or coverage-unknown line:\n%s", text)
	}
	out.Reset()
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"a.go"}, Run: "^TestOK$", Runs: 1, KilledRuns: 1, Killed: true, Killer: "TestOK",
	})
	if text := out.String(); strings.Contains(text, "unexercised") {
		t.Fatalf("kill verdict rendered a label it should not carry:\n%s", text)
	}
}

// The effective oracle bound and its measured input are part of the
// verdict's meaning — a timeout kill under a 60s budget and one under
// 40m are different claims — so the render surfaces both
// (REQ-exec-ephemeral's derived budget: the result reports the
// effective budget either way).
func TestRenderEphemeralVerdictReportsOracleBudget(t *testing.T) {
	var out bytes.Buffer
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"a.go"}, Run: "^TestOK$", Runs: 1, KilledRuns: 1, Killed: true, Killer: "TestOK",
		OracleBudget: "1m4s", MeasuredBaseline: "16s",
	})
	if text := out.String(); !strings.Contains(text, "oracle budget 1m4s") || !strings.Contains(text, "baseline measured 16s") {
		t.Fatalf("render missing the effective budget or its measured input:\n%s", text)
	}
}

// 0 is the ephemeral face's oracle-timeout default: the budget derives
// from the measured baseline, and an explicit value is the override
// (REQ-exec-ephemeral's derived budget).
func TestEphemeralOracleTimeoutDefaultsToDerived(t *testing.T) {
	cmd := newEphemeralCommand()
	if got := cmd.Flags().Lookup("oracle-timeout"); got == nil || got.DefValue != "0s" {
		t.Fatalf("--oracle-timeout = %+v, want the derive-from-baseline default (0s)", got)
	}
}

// syncWriter is the run face's one serialization point for the
// heartbeat's concurrency-exempt callback beside the callback-locked
// render lines; concurrent writes must stay whole and race-free.
func TestSyncWriterSerializesConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	w := &syncWriter{w: &buf}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				fmt.Fprintf(w, "line\n")
			}
		}()
	}
	wg.Wait()
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != "line" {
			t.Fatalf("sheared line %q", line)
		}
	}
}

// An attested survivor's verdict names the attestation — its digest,
// provenance, and reasoning — in place of the bare survival
// (REQ-result-ephemeral-attest).
func TestEphemeralVerdictNamesTheAttestation(t *testing.T) {
	var out strings.Builder
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"lib/lib.go"}, Run: "^TestWeak$",
		Attested: &gomutant.EphemeralAttestation{EditDigest: "0123456789abcdef", TestPkg: "example.com/p", Run: "^TestStrong$", Commit: "fedcba9876543210", Dirty: true, Reason: "known-surviving by design"},
	})
	text := out.String()
	if !strings.Contains(text, "SURVIVED  lib/lib.go  — attested 0123456789ab at fedcba987654, dirty under example.com/p ^TestStrong$: known-surviving by design") {
		t.Fatalf("verdict = %q, want the attestation named", text)
	}
	// A row keyed on another digest form names the form beside the
	// digest, so the digest shown can be found in the record.
	out.Reset()
	renderEphemeralVerdict(&out, &gomutant.EphemeralResult{
		Files: []string{"lib/lib.go"}, Run: "^TestWeak$",
		Attested: &gomutant.EphemeralAttestation{EditDigest: "0123456789abcdef", DigestForm: "raw", TestPkg: "example.com/p", Run: "^TestStrong$", Reason: "legacy judgment"},
	})
	if !strings.Contains(out.String(), "attested 0123456789ab [raw] at dirty tree under example.com/p ^TestStrong$: legacy judgment") {
		t.Fatalf("verdict = %q, want the raw-keyed row's form named", out.String())
	}
}

// The command attests a surviving probe once; a second --attest of the
// same mutant refuses before any measurement with the standing row
// named, and --reattest replaces it (REQ-result-ephemeral-attest).
func TestEphemeralCommandAttestsOnceAndReattestsByName(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	inside, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(inside), "return x - 1", "return x - 2", 1)
	rep := filepath.Join(t.TempDir(), "lib.go")
	if err := os.WriteFile(rep, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := filepath.Join(t.TempDir(), "findings.json")
	first := ephemeralOptions{dir: dir, file: "lib/lib.go", replacement: rep, testPkg: "example.com/fixture/lib", runPat: "^TestWeak$", findingsFile: findings, attest: "untested large-x branch: known-surviving by fixture design"}
	if err := ephemeralCommand(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	var already *gomutant.ErrAlreadyAttested
	if err := ephemeralCommand(context.Background(), first); !errors.As(err, &already) || !strings.Contains(already.Reason, "known-surviving") {
		t.Fatalf("second attest = %v, want the standing row refused before measurement", err)
	}
	third := first
	third.attest = "re-judged on a sharper ground"
	third.reattest = true
	var out bytes.Buffer
	third.output = &out
	if err := ephemeralCommand(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "equivalence recorded") || !strings.Contains(out.String(), "(supersedes ") || !strings.Contains(out.String(), "known-surviving") {
		t.Fatalf("re-attest output = %q, want the recorded line naming the superseded row", out.String())
	}
	atts, err := gomutant.LoadEphemeralAttestations(gomutant.EphemeralAttestationsPathFor(findings))
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Reason != "re-judged on a sharper ground" {
		t.Fatalf("record = %+v, want the one row replaced", atts)
	}
}
