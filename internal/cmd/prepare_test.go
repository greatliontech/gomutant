package cmd

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// unloadable returns a tree root that exists but cannot load — a
// go.mod that parses as nothing — so a verb that reaches the load
// fails with the load's own error, and any other error seen fired
// before it.
func unloadable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("this is not a module file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The refusals a verb's inputs alone decide fire before any tree load
// (REQ-exec-preparation): every case below runs against a tree root
// that cannot load, so the error seen is the refusal, which means it
// fired first; a root that does not exist refuses as an input too.
func TestRunRefusesItsInputsBeforeAnyLoad(t *testing.T) {
	nowhere := unloadable(t)
	doc := filepath.Join(t.TempDir(), "findings.json")
	document, err := gomutant.Export(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, document, 0o644); err != nil {
		t.Fatal(err)
	}
	refuses := func(name string, o runOptions, want string) {
		t.Helper()
		o.dir, o.output = nowhere, os.Stderr
		if o.findingsFile == "" {
			o.findingsFile = doc
		}
		err := runCommand(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want a refusal containing %q before any load", name, err, want)
		}
	}
	refuses("negative budget", runOptions{budget: -1}, "budget must be non-negative")
	refuses("negative oracle timeout", runOptions{oracleTimeout: -time.Second}, "oracle timeout must")
	refuses("targets and changed", runOptions{targetsFile: doc, changed: "HEAD"}, "--targets and --changed were given")
	refuses("inline targets document", runOptions{targetsFile: "{\"targets\":[]}"}, "looks like an inline JSON document")
	refuses("malformed scratch namespace", runOptions{scratchNamespaces: []string{"no-colon"}}, "scratch")
	refuses("bracket path escaping the tree", runOptions{bracketPaths: []string{"../outside"}}, "escapes the tree root")
	refuses("absent bracket path", runOptions{bracketPaths: []string{"no-such-surface"}}, "does not exist at run start")
	refuses("malformed vouch", runOptions{vouches: []string{"not-an-identity"}}, "vouch")
	// A missing root refuses before the lock, whose file would have
	// created it: the findings path lies under the root, so creation
	// would be visible.
	missingRoot := filepath.Join(t.TempDir(), "missing")
	missing := runOptions{dir: missingRoot, findingsFile: filepath.Join(missingRoot, ".gomutant", "findings.json")}
	if err := runCommand(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "tree root") {
		t.Fatalf("missing root: %v; want the root refusal before any lock or load", err)
	}
	if _, err := os.Stat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("a refused run created its root: %v", err)
	}
	// A malformed exemptions record and a findings document from a
	// later binary refuse before any load.
	exemptDir := unloadable(t)
	exemptDoc := filepath.Join(exemptDir, ".gomutant", "findings.json")
	if err := os.MkdirAll(filepath.Dir(exemptDoc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exemptDoc, document, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gomutant.ExemptionsPathFor(exemptDoc), []byte("{ torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), runOptions{dir: exemptDir, findingsFile: exemptDoc, output: os.Stderr}); err == nil || !strings.Contains(err.Error(), "exemption") {
		t.Fatalf("malformed exemptions: %v; want the record's refusal before any load", err)
	}
	if err := os.Remove(gomutant.ExemptionsPathFor(exemptDoc)); err != nil {
		t.Fatal(err)
	}
	ahead := regexp.MustCompile(`"version":\s*\d+`).ReplaceAll(document, []byte(`"version": 999`))
	if err := os.WriteFile(exemptDoc, ahead, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), runOptions{dir: exemptDir, findingsFile: exemptDoc, output: os.Stderr}); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version-ahead findings document: %v; want the document's refusal before any load", err)
	}
	// A held campaign lock refuses at preparation, naming the holder.
	release, err := gomutant.AcquireCampaignLock(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	refuses("held campaign lock", runOptions{}, "a campaign already holds")
	// A plan takes no lock, so it reaches the load — and fails there,
	// on the unloadable tree, not on the lock.
	err = runCommand(context.Background(), runOptions{dir: nowhere, findingsFile: doc, plan: true, output: os.Stderr})
	if err == nil || !strings.Contains(err.Error(), "loading Go packages") {
		t.Fatalf("plan under a held lock: %v; want the load's own failure, never the lock's", err)
	}
}

// The ephemeral verb's run count and attestation reasoning are refused
// before the load and the probe (REQ-exec-preparation).
func TestEphemeralRefusesItsInputsBeforeAnyLoad(t *testing.T) {
	nowhere := unloadable(t)
	base := ephemeralOptions{dir: nowhere, testPkg: "example.com/p", runPat: "^TestX$", file: "p/p.go", replacement: "p/p.go"}
	// No edit form given: the runs refusal precedes the form checks, the
	// order the MCP face keeps.
	tooMany := base
	tooMany.runs, tooMany.file, tooMany.replacement = gomutant.MaxEphemeralRuns+1, "", ""
	if err := ephemeralCommand(context.Background(), tooMany); err == nil || !strings.Contains(err.Error(), "is outside 1-") {
		t.Fatalf("runs out of range: %v; want the refusal before any load", err)
	}
	blank := base
	blank.runs, blank.attest = 1, "   "
	if err := ephemeralCommand(context.Background(), blank); err == nil || !strings.Contains(err.Error(), "needs its reasoning") {
		t.Fatalf("blank attestation reason: %v; want the refusal before any load", err)
	}
}

// The retarget pair's shape is refused before the store and the load
// (REQ-exec-preparation).
func TestRetargetRefusesItsPairBeforeAnyLoad(t *testing.T) {
	nowhere := unloadable(t)
	for name, o := range map[string]retargetOptions{
		"same prefixes":   {dir: nowhere, from: "example.com/p", to: "example.com/p"},
		"unlike shapes":   {dir: nowhere, from: "example.com/p", to: "example.com/p.F"},
		"unlike terminal": {dir: nowhere, from: "example.com/old.", to: "example.com/new"},
	} {
		err := retargetCommand(context.Background(), o, os.Stderr)
		if err == nil || !strings.Contains(err.Error(), "retarget") {
			t.Fatalf("%s: %v; want the pair refusal before any load", name, err)
		}
	}
}

// The discover verb refuses two target sources before the load, as run
// does (REQ-exec-preparation's "every verb").
func TestDiscoverRefusesTwoTargetSourcesBeforeAnyLoad(t *testing.T) {
	nowhere := unloadable(t)
	err := discoverCommand(context.Background(), discoverOptions{dir: nowhere, targetsFile: "targets.json", changed: "HEAD"})
	if err == nil || !strings.Contains(err.Error(), "--targets and --changed were given") {
		t.Fatalf("discover with two sources: %v; want the refusal before any load", err)
	}
}
