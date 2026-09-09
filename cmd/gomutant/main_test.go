package main

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/greatliontech/gomutant/internal/mcpserver"
)

// The exit status is the tree's own where the error carries one, found
// through wrapping — a session ended on its transport exits 2 — and the
// one failure code for every other error, a wrapped subprocess's own
// status included (REQ-mcp-exit-log).
func TestExitCodeIsTheTreesOwn(t *testing.T) {
	transport := &mcpserver.ExitError{Class: mcpserver.ExitTransport, Cause: errors.New("wire torn")}
	if got := exitCode(transport); got != 2 {
		t.Fatalf("transport exit = %d, want 2", got)
	}
	if got := exitCode(fmt.Errorf("mcp: %w", transport)); got != 2 {
		t.Fatalf("wrapped transport exit = %d, want 2", got)
	}
	if got := exitCode(errors.New("no code")); got != 1 {
		t.Fatalf("plain error exit = %d, want 1", got)
	}
	// A git or go subprocess's failure reaches main wrapped: its own
	// status (128 for git's ambiguous argument, -1 for a signal) is never
	// the command's.
	err := exec.Command("sh", "-c", "exit 128").Run()
	var sub *exec.ExitError
	if !errors.As(err, &sub) || sub.ExitCode() != 128 {
		t.Fatalf("fixture subprocess = %v", err)
	}
	if got := exitCode(fmt.Errorf("git diff: %w", err)); got != 1 {
		t.Fatalf("wrapped subprocess exit = %d, want the tree's one failure code", got)
	}
}
