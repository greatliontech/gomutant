package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A drift refusal folds its attestation sheds into the error text - the
// SDK renders only the error on failure and the document already
// stripped them, so the text is the sheds' one surfacing
// (REQ-attest-survivor).
func TestDriftErrorCarriesSheds(t *testing.T) {
	base := fmt.Errorf("tree drifted")
	if err := driftError(base, nil); err != base {
		t.Fatalf("shed-free drift rewrapped: %v", err)
	}
	err := driftError(base, []string{"p.F f.go:1:1 zero-return: killed by TestF"})
	if err == nil || !strings.Contains(err.Error(), "tree drifted") || !strings.Contains(err.Error(), "re-attest if genuinely equivalent") || !strings.Contains(err.Error(), "killed by TestF") {
		t.Fatalf("drift error missing sheds: %v", err)
	}
}

// withHeartbeat emits still-working notifications on the caller's ONE
// notifier while the stretch runs - and stays silent without one
// (REQ-mcp-envelope's no-silent-stretch clause for tree loads and
// oracle stretches).
func TestWithHeartbeatNotifiesDuringTheStretch(t *testing.T) {
	prior := heartbeatInterval
	heartbeatInterval = 5 * time.Millisecond
	defer func() { heartbeatInterval = prior }()
	var beats atomic.Int64
	notify := func(string) { beats.Add(1) }
	got, err := withHeartbeat(context.Background(), notify, "probe", func(context.Context) (int, error) {
		time.Sleep(60 * time.Millisecond)
		return 7, nil
	})
	if err != nil || got != 7 {
		t.Fatalf("withHeartbeat = %d, %v", got, err)
	}
	if beats.Load() == 0 {
		t.Fatal("no still-working notification during a slow stretch")
	}
	// A labelled stretch names the phase current at each beat.
	var labels []string
	var mu sync.Mutex
	label := atomic.Value{}
	label.Store("baseline")
	if _, err := withHeartbeatLabel(context.Background(), func(m string) { mu.Lock(); labels = append(labels, m); mu.Unlock() }, func() string { return label.Load().(string) }, func(context.Context) (int, error) {
		time.Sleep(4 * heartbeatInterval)
		label.Store("mutant-run 1/1")
		time.Sleep(4 * heartbeatInterval)
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	joined := strings.Join(labels, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "still working: baseline") || !strings.Contains(joined, "still working: mutant-run 1/1") {
		t.Fatalf("heartbeat labels = %q", labels)
	}
	if _, err := withHeartbeat(context.Background(), nil, "probe", func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("nil-notifier stretch failed: %v", err)
	}
}

// The targets document is an input on this face too: run and discover
// parse the inline document, or the confined path's, before the tree
// loads, so a malformed one refuses with its own error and never pays a
// load (REQ-exec-preparation).
func TestToolRunParsesTheTargetsDocumentBeforeTheLoad(t *testing.T) {
	dir := t.TempDir()
	// A go.mod the go command refuses: a load here fails for its own
	// reason, so the document's refusal is only reachable before it.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.26.4\n\nrequire (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if _, _, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"nope":[]}`}); err == nil || !strings.Contains(err.Error(), "parse targets document") {
		t.Fatalf("run refused with %v, want the document's own refusal before the load", err)
	}
	// Refused before the lock: nothing of the campaign's state was minted.
	if _, err := os.Stat(filepath.Join(dir, ".gomutant")); !os.IsNotExist(err) {
		t.Fatalf("a document refusal minted the campaign directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "targets.json"), []byte(`{"nope":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsPath: "targets.json"}); err == nil || !strings.Contains(err.Error(), "parse targets document") {
		t.Fatalf("discover refused with %v, want the document's own refusal before the load", err)
	}
	// The path is confined to the tree before it is read (REQ-mcp-envelope).
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "escape.json"), []byte(`{"targets":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, escape := range []string{"../escape.json", filepath.Join(filepath.Dir(dir), "escape.json")} {
		if _, _, err := s.toolRun(context.Background(), nil, runIn{TargetsPath: escape}); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
			t.Fatalf("run read %q: %v, want the confinement refusal", escape, err)
		}
		if _, _, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsPath: escape}); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
			t.Fatalf("discover read %q: %v, want the confinement refusal", escape, err)
		}
	}
}
