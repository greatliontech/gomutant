package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	path := findingsAt(dir, defaultFindings)
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
	judgeAttestedPosture(context.Background(), tree, vouches, gomutant.Finding{Symbol: "example.com/fixture/lib.Add"})
	if got := tree.DynamicStateVouches(); len(got) != 1 || got[0] != vouches[0] {
		t.Fatalf("the tree judged under %v, want %v", got, vouches)
	}
}
