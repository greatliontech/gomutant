package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

// A malformed --vouch refuses the attestation before the write, as
// every declaration's shape does; a well-formed one reaches the
// posture judgment (REQ-exec-preparation).
func TestAttestRefusesAMalformedVouchBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedFinding(t, dir, "example.com/empty.Old", "example.com/empty.TestOld", "example.com/empty")
	path := gomutant.FindingsPathAt(dir, defaultFindings)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = attestCommand(context.Background(), attestOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.Old", position: "lib/lib.go:1:1", operator: "zero return", reason: "r", vouches: []string{"no-colon"}}, &out)
	if err == nil || !strings.Contains(err.Error(), "vouch") {
		t.Fatalf("attest with a malformed vouch = %v, want the vouch's refusal", err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a refused attest changed the document: %v", err)
	}
}

// A whitespace-only reasoning is a declaration's shape: refused before
// the document lock, so a fresh tree gains no .gomutant directory
// (REQ-exec-preparation).
func TestAttestRefusesABlankReasonBeforeTheLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := attestCommand(context.Background(), attestOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.Old", position: "lib/lib.go:1:1", operator: "zero return", reason: "   "}, &out)
	if err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("attest with a blank reasoning = %v, want the reasoning refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gomutant")); !os.IsNotExist(err) {
		t.Fatalf("a refused attest persisted the document's directory: %v", err)
	}
}

// The vouches reach the tree the posture is judged on — the line the
// face-parity fix rests on.
func TestAttestedPostureJudgesUnderTheVouches(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	dir := isolatedFixture(t)
	tree, err := gomutant.LoadContextSelection(context.Background(), dir, gomutant.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	vouches := []string{"example.com/dep.Var"}
	judgeAttestedPosture(context.Background(), tree, vouches, gomutant.Finding{Symbol: "example.com/fixture/lib.Add"}, nil)
	if got := tree.DynamicStateVouches(); len(got) != 1 || got[0] != vouches[0] {
		t.Fatalf("the tree judged under %v, want %v", got, vouches)
	}
}

// lockedBuffer is a buffer a test reads while a verb's cadence writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The attest verb's cadence names the preparation while the document
// lock waits behind another writer: a held lock is a stretch the line
// names, never a silence (REQ-exec-run-status).
func TestAttestNamesThePreparationWhileTheDocumentLockWaits(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	fastCadence(t)
	dir := isolatedFixture(t)
	seedFinding(t, dir, "example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd", "example.com/fixture/lib")
	held, release := make(chan struct{}), make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- gomutant.UpdateDocument(context.Background(), gomutant.FindingsPathAt(dir, defaultFindings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
			close(held)
			<-release
			return all, nil
		})
	}()
	<-held
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- attestCommand(context.Background(), attestOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/fixture/lib.Add", position: "lib/lib.go:1:1", operator: "zero return", reason: "equivalent by inspection"}, &out)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "progress  "+gomutant.StretchPreparation+", elapsed ") {
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("no preparation line while the document lock waited: %q", out.String())
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "attested lib/lib.go:1:1 zero return") {
		t.Fatalf("attest echo after the lock released = %q", out.String())
	}
}
