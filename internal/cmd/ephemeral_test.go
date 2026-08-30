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
