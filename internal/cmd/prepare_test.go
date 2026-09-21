package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
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
	refuses("negative analysis budget", runOptions{analysisBudget: -time.Second}, "analysis budget must")
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

// The targets document is an input: run and discover parse it before
// the tree loads, so a malformed document refuses with its own error
// and never pays a load (REQ-exec-preparation).
func TestTargetsDocumentIsParsedBeforeTheLoad(t *testing.T) {
	dir := t.TempDir()
	// A go.mod the go command refuses: a load here fails for its own
	// reason, so the document's refusal is only reachable before it.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.26.4\n\nrequire (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"nope":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, targetsFile: targetsPath, output: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "parse targets document") {
		t.Fatalf("run refused with %v, want the document's own refusal before the load", err)
	}
	// Refused before the lock: nothing of the campaign's state was minted.
	if _, err := os.Stat(filepath.Join(dir, ".gomutant")); !os.IsNotExist(err) {
		t.Fatalf("a document refusal minted the campaign directory: %v", err)
	}
	_, err = discoverTargets(context.Background(), discoverOptions{dir: dir, targetsFile: targetsPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "parse targets document") {
		t.Fatalf("discover refused with %v, want the document's own refusal before the load", err)
	}
}

// The findings verb refuses what its inputs decide before it reads
// anything: the state's spelling before the store opens, a malformed
// vouch before the tree loads (REQ-exec-preparation).
func TestFindingsRefusesItsInputsBeforeAnyRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/broken\n\ngo 1.26.4\n\nrequire (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := gomutant.FindingsPathAt(dir, defaultFindings)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An exemptions record the store refuses at open: the state's
	// refusal must come before the store opens.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, state: "recorded"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `unknown state "recorded"`) {
		t.Fatalf("an unknown state beside an unopenable store = %v, want the state refused first", err)
	}
	if err := os.Remove(gomutant.ExemptionsPathFor(path)); err != nil {
		t.Fatal(err)
	}
	// A record, so the verb would load the tree; a malformed vouch
	// beside a tree that cannot load: the vouch's refusal, never the
	// load's — and with a well-formed vouch the load's own.
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: symbol}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
	}
	seed := gomutant.Finding{Symbol: "example.com/broken.F", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		TargetEvidence: evidence("example.com/broken.F"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/broken.TestF")}}
	if err := gomutant.UpdateDocument(context.Background(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{seed}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, vouches: []string{"example.com/dep:Var"}}, io.Discard); err == nil || strings.Contains(err.Error(), "vouch") {
		t.Fatalf("a well-formed vouch under an unloadable tree = %v, want the load's own refusal", err)
	}
	// What rides beside the rows is read with the records: an unreadable
	// attestation record refuses before any row renders or judges.
	if err := os.WriteFile(gomutant.EphemeralAttestationsPathFor(path), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rows bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings}, &rows); err == nil || rows.Len() != 0 {
		t.Fatalf("an unreadable attestation record: %v with %q rendered; want the refusal before any row", err, rows.String())
	}
	if err := os.Remove(gomutant.EphemeralAttestationsPathFor(path)); err != nil {
		t.Fatal(err)
	}
	// The build selection's shape refuses before any record is read, on
	// the zero-match path too.
	err = findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/broken.Nope", toolchain: "not a toolchain"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not a toolchain") {
		t.Fatalf("a malformed toolchain beside an emptying filter = %v, want the selection refused", err)
	}
	err = findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, vouches: []string{"no-colon-no-dot"}}, io.Discard)
	if err == nil || strings.Contains(err.Error(), "go.mod") || !strings.Contains(err.Error(), "vouch") {
		t.Fatalf("a malformed vouch beside an unloadable tree = %v, want the vouch refused before the load", err)
	}
}
