//go:build unix

package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The field wedge of the protodb campaign filing: a writer killed
// between lockfile creation and removal left a marker every later
// session refused on, prescribing hand-removal. The flock is the lock -
// a crashed holder's residue holds nothing, so an update proceeds
// through it without operator work (REQ-exec-exclusivity's liveness
// discipline).
func TestDocumentLockAbsorbsCrashedHolderResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	// A crashed holder's residue: content but no flock behind it.
	if err := os.WriteFile(path+".lock", []byte("pid 999999999 since 2026-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := UpdateDocument(path, func(p []Finding) ([]Finding, error) { ran = true; return p, nil }); err != nil {
		t.Fatalf("update blocked by crashed-holder residue: %v", err)
	}
	if !ran {
		t.Fatal("update never ran")
	}
}

// A refusal implies a live holder, so it names the holder instead of
// prescribing marker removal (REQ-exec-exclusivity).
func TestDocumentLockRefusalNamesLiveHolder(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the lock retry budget")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	// Garbage residue in the lockfile, deliberately LONGER than any
	// holder line: the acquire must truncate before writing, so a
	// write-without-truncate leaves the residue's tail readable past
	// the fresh line and the sentinel below catches it.
	residue := strings.Repeat("x", 128) + " stale garbage residue\n"
	if err := os.WriteFile(path+".lock", []byte(residue), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := acquireDocumentLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireDocumentLock(context.Background(), path)
	if err == nil {
		t.Fatal("second acquisition succeeded under a live holder")
	}
	if !strings.Contains(err.Error(), "live session") || !strings.Contains(err.Error(), "pid ") {
		t.Fatalf("refusal = %v; want the live holder named", err)
	}
	if strings.Contains(err.Error(), "stale garbage") {
		t.Fatalf("refusal = %v; names pre-acquire residue instead of the live holder", err)
	}
	if strings.Contains(err.Error(), "remove") {
		t.Fatalf("refusal = %v; hand-removal guidance has no place under a live holder", err)
	}
	release()
	release2, err := acquireDocumentLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquisition after release: %v", err)
	}
	release2()
}
