package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureDir = "../engine/testdata/fixturemod"

func TestEphemeralBatchOptions(t *testing.T) {
	if err := ephemeralCommand(ephemeralOptions{batch: "batch.json", file: "x.go", testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "omit --file") {
		t.Fatalf("batch with file accepted: %v", err)
	}
	if err := ephemeralCommand(ephemeralOptions{testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("missing mutation form accepted: %v", err)
	}
	if err := ephemeralCommand(ephemeralOptions{dir: "missing", replacement: "r.go", testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "needs --file") {
		t.Fatalf("replacement without file reached tree loading: %v", err)
	}
	path := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(`{"edits":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ephemeralCommand(ephemeralOptions{dir: t.TempDir(), batch: path, testPkg: "p", runPat: "T"}); err == nil || !strings.Contains(err.Error(), "edit batch is empty") {
		t.Fatalf("empty batch accepted: %v", err)
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
	err = ephemeralCommand(ephemeralOptions{dir: fixtureDir, batch: path, testPkg: "example.com/fixture/lib", runPat: "^TestAdd$"})
	if err != nil {
		t.Fatal(err)
	}
}
