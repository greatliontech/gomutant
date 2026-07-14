package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
