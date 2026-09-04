package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The run tool fires every input-decidable refusal before the tree load
// (REQ-exec-preparation), exactly as the CLI does: each case runs the
// server over a root that exists but cannot load, so the error seen is
// the refusal; a root that does not exist refuses as an input too.
func TestRunToolRefusesItsInputsBeforeAnyLoad(t *testing.T) {
	nowhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(nowhere, "go.mod"), []byte("this is not a module file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nowhere, ".gomutant"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(nowhere, defaultFindings)
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, document, 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(nowhere)
	refuses := func(name string, in runIn, want string) {
		t.Helper()
		_, _, err := s.toolRun(context.Background(), nil, in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want a refusal containing %q before any load", name, err, want)
		}
	}
	refuses("negative budget", runIn{Budget: -1}, "budget must be non-negative")
	refuses("targets and changed", runIn{TargetsPath: doc, Changed: "HEAD"}, "targets_path and changed were given")
	refuses("inline targets and changed", runIn{TargetsJSON: "[]", Changed: "HEAD"}, "targets_json and changed were given")
	refuses("two targets documents", runIn{TargetsPath: doc, TargetsJSON: "[]"}, "targets_path and targets_json were given")
	refuses("malformed scratch namespace", runIn{ScratchNamespaces: []string{"no-colon"}}, "scratch")
	missingRoot := filepath.Join(t.TempDir(), "missing")
	if _, _, err := New(missingRoot).toolRun(context.Background(), nil, runIn{}); err == nil || !strings.Contains(err.Error(), "tree root") {
		t.Fatalf("missing root: %v; want the root refusal before any lock or load", err)
	}
	if _, err := os.Stat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("a refused run created its root (the lock's directory): %v", err)
	}
	if _, _, err := s.toolRetarget(context.Background(), nil, retargetIn{From: "example.com/p", To: "example.com/p"}); err == nil || !strings.Contains(err.Error(), "retarget") {
		t.Fatalf("retarget same prefixes: %v; want the pair refusal before any load", err)
	}
	if _, _, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsPath: doc, Changed: "HEAD"}); err == nil || !strings.Contains(err.Error(), "targets_path and changed were given") {
		t.Fatalf("discover with two sources: %v; want the refusal before any load", err)
	}
	probe := ephemeralIn{File: "p/p.go", Replacement: "package p", TestPkg: "example.com/p", Run: "^TestX$"}
	tooMany := probe
	tooMany.Runs = gomutant.MaxEphemeralRuns + 1
	if _, _, err := s.toolEphemeral(context.Background(), nil, tooMany); err == nil || !strings.Contains(err.Error(), "is outside 1-") {
		t.Fatalf("ephemeral runs out of range: %v; want the refusal before any load", err)
	}
	blank := probe
	blank.Attest = "   "
	if _, _, err := s.toolEphemeral(context.Background(), nil, blank); err == nil || !strings.Contains(err.Error(), "needs its reasoning") {
		t.Fatalf("ephemeral blank attestation reason: %v; want the refusal before any load or probe", err)
	}
	release, err := gomutant.AcquireCampaignLock(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	refuses("held campaign lock", runIn{}, "a campaign already holds")
}
