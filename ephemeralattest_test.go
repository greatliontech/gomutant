package gomutant

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The committed ephemeral-equivalence record round-trips: recording
// appends digest-sorted entries, re-attesting an identical edit digest
// replaces the prior reasoning, and a missing file is an empty record
// (REQ-result-ephemeral-attest).
func TestEphemeralAttestationRecordRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	if atts, err := LoadEphemeralAttestations(path); err != nil || atts != nil {
		t.Fatalf("missing record = %v, %v; want an empty record", atts, err)
	}
	first := EphemeralAttestation{EditDigest: "bbb", Files: []string{"a.go"}, TestPkg: "example.com/p", Run: "^TestA$", Reason: "defense-in-depth nil guard"}
	second := EphemeralAttestation{EditDigest: "aaa", Files: []string{"b.go"}, TestPkg: "example.com/p", Run: "^TestB$", Reason: "union-vs-dispatch equivalence"}
	if err := RecordEphemeralAttestation(context.Background(), path, first); err != nil {
		t.Fatal(err)
	}
	if err := RecordEphemeralAttestation(context.Background(), path, second); err != nil {
		t.Fatal(err)
	}
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[0].EditDigest != "aaa" || atts[1].EditDigest != "bbb" {
		t.Fatalf("record = %+v, want two digest-sorted entries", atts)
	}
	rejudged := first
	rejudged.Reason = "re-judged: still equivalent, sharper ground"
	if err := RecordEphemeralAttestation(context.Background(), path, rejudged); err != nil {
		t.Fatal(err)
	}
	atts, err = LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[1].Reason != rejudged.Reason {
		t.Fatalf("re-attested record = %+v, want the identical digest's reasoning replaced", atts)
	}
}

// A malformed or incomplete record refuses rather than serving partial
// authority (REQ-result-ephemeral-attest).
func TestEphemeralAttestationRecordRefusesMalformedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"attestations":[{"editDigest":"x","files":["f.go"],"testPkg":"p","run":"^T$"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEphemeralAttestations(path); err == nil || !strings.Contains(err.Error(), "needs editDigest, files, testPkg, run, and reason") {
		t.Fatalf("reason-free entry = %v, want the validation refusal", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"attestations":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEphemeralAttestations(path); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("future version = %v, want the version refusal", err)
	}
}

// Attestation is refused for every probe state that is not an exercised
// full survivor: killed, mixed, unexercised, unreasoned, and
// digest-less results each name their ground — evidence beats
// attestation, and vacuous evidence attests nothing
// (REQ-result-ephemeral-attest).
func TestAttestEphemeralEquivalenceRefusals(t *testing.T) {
	ctx := context.Background()
	base := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1"}
	killed := base
	killed.Killed, killed.KilledRuns = true, 1
	if _, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &killed, "why"); err == nil || !strings.Contains(err.Error(), "evidence beats attestation") {
		t.Fatalf("killed probe attested: %v", err)
	}
	mixed := base
	mixed.Runs, mixed.KilledRuns = 3, 1
	if _, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &mixed, "why"); err == nil || !strings.Contains(err.Error(), "1 of 3 runs") {
		t.Fatalf("mixed probe attested: %v", err)
	}
	unexercised := base
	unexercised.UnexercisedFiles = []string{"f.go"}
	if _, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &unexercised, "why"); err == nil || !strings.Contains(err.Error(), "vacuous evidence") {
		t.Fatalf("unexercised probe attested: %v", err)
	}
	if _, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &base, "  "); err == nil || !strings.Contains(err.Error(), "reasoning on the record") {
		t.Fatalf("unreasoned attestation accepted: %v", err)
	}
	digestless := base
	digestless.EditDigest = ""
	if _, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &digestless, "why"); err == nil || !strings.Contains(err.Error(), "no edit digest") {
		t.Fatalf("digest-less attestation accepted: %v", err)
	}
	att, err := AttestEphemeralEquivalence(ctx, t.TempDir(), &base, "genuinely equivalent")
	if err != nil {
		t.Fatal(err)
	}
	if att.EditDigest != "d1" || att.Reason != "genuinely equivalent" || att.TestPkg != "example.com/p" || att.Commit != "" {
		t.Fatalf("attestation = %+v, want the probe's identity carried and no commit without provenance", att)
	}
}

// End to end: a real surviving probe attests into the committed record
// with the digest the result carried, and the digest is deterministic —
// the identical edit probes to the identical identity
// (REQ-result-ephemeral-attest).
func TestEphemeralAttestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	inside, err := os.ReadFile("internal/engine/testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(inside), "return x - 1", "return x - 2", 1)
	if mutated == string(inside) {
		t.Fatal("fixture edit failed")
	}
	res, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(mutated), "example.com/fixture/lib", "^TestWeak$", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed || len(res.UnexercisedFiles) != 0 || res.EditDigest == "" {
		t.Fatalf("probe = %+v, want an exercised survivor carrying its digest", res)
	}
	again, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(mutated), "example.com/fixture/lib", "^TestWeak$", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if again.EditDigest != res.EditDigest {
		t.Fatalf("digest not deterministic: %s vs %s", again.EditDigest, res.EditDigest)
	}
	att, err := AttestEphemeralEquivalence(ctx, fixtureDir, res, "untested large-x branch: known-surviving by fixture design")
	if err != nil {
		t.Fatal(err)
	}
	path := EphemeralAttestationsPathFor(filepath.Join(t.TempDir(), "findings.json"))
	if err := RecordEphemeralAttestation(context.Background(), path, att); err != nil {
		t.Fatal(err)
	}
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].EditDigest != res.EditDigest || atts[0].TestPkg != "example.com/fixture/lib" || atts[0].Run != "^TestWeak$" {
		t.Fatalf("recorded = %+v, want the probe's identity on the record", atts)
	}
}

// A survivor whose exercise state is unknown — the coverage probe
// failed, carried distinctly on the result — refuses attestation: the
// absent unexercised label is not evidence of exercise
// (REQ-result-ephemeral-attest).
func TestAttestEphemeralEquivalenceRefusesUnknownCoverage(t *testing.T) {
	unknown := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1", CoverageUnknown: true}
	if _, err := AttestEphemeralEquivalence(context.Background(), t.TempDir(), &unknown, "why"); err == nil || !strings.Contains(err.Error(), "exercise state is unknown") {
		t.Fatalf("unknown-coverage probe attested: %v", err)
	}
}

// The write validates with the load's own predicate: an invalid row
// can never land as a poison pill that makes the record unloadable
// (REQ-result-ephemeral-attest).
func TestRecordEphemeralAttestationRefusesInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	bad := EphemeralAttestation{EditDigest: "d", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$"}
	if err := RecordEphemeralAttestation(context.Background(), path, bad); err == nil || !strings.Contains(err.Error(), "needs editDigest, files, testPkg, run, and reason") {
		t.Fatalf("reason-free row written: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused write left a record file: %v", err)
	}
}

// The provenance stamp records commit AND dirty over the replaced
// files: clean at HEAD stamps clean, an uncommitted edit of a replaced
// file stamps dirty (the commit then names the nearest ancestor), and
// no repository stamps fail-closed dirty with no commit
// (REQ-result-ephemeral-attest).
func TestAttestEphemeralEquivalenceStampsDirtyProvenance(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", ".")
	runGit("commit", "-q", "-m", "fixture")
	res := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1"}
	clean, err := AttestEphemeralEquivalence(context.Background(), root, &res, "why")
	if err != nil {
		t.Fatal(err)
	}
	if clean.Commit == "" || clean.Dirty {
		t.Fatalf("clean-tree stamp = %+v, want commit with dirty=false", clean)
	}
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("package p\n\nvar edited = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := AttestEphemeralEquivalence(context.Background(), root, &res, "why")
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Commit == "" || !dirty.Dirty {
		t.Fatalf("dirty-tree stamp = %+v, want the commit with dirty=true", dirty)
	}
	// No repository (the refusals test already pins Commit==""): the
	// fail-closed direction is dirty.
	bare, err := AttestEphemeralEquivalence(context.Background(), t.TempDir(), &res, "why")
	if err != nil {
		t.Fatal(err)
	}
	if bare.Commit != "" || !bare.Dirty {
		t.Fatalf("no-repository stamp = %+v, want fail-closed dirty with no commit", bare)
	}
	// The shipped faces pass a RELATIVE dir (default "."): the judged
	// paths must absolutize, or a pristine tree stamps dirty and the
	// clean case is unreachable from either face.
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	relative, err := AttestEphemeralEquivalence(context.Background(), ".", &res, "why")
	if err != nil {
		t.Fatal(err)
	}
	if relative.Commit == "" || relative.Dirty {
		t.Fatalf("relative-dir clean stamp = %+v, want commit with dirty=false", relative)
	}
	// A cancelled attest refuses instead of persisting a fail-closed
	// row as success.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AttestEphemeralEquivalence(cancelled, root, &res, "why"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled attest = %v, want context.Canceled", err)
	}
}

// The edit digest discriminates: distinct content is a distinct
// identity, the same content in a different file is a distinct
// identity, and the derivation is deterministic and alias-collapsed
// (the resolved tree-relative path keys the entry)
// (REQ-result-ephemeral-attest).
func TestEphemeralEditDigestDiscriminates(t *testing.T) {
	dir := t.TempDir()
	a := fileReplacement{File: "a.go", Abs: filepath.Join(dir, "a.go"), Source: []byte("package p\nvar x = 1\n")}
	b := fileReplacement{File: "b.go", Abs: filepath.Join(dir, "b.go"), Source: []byte("package p\nvar x = 1\n")}
	aChanged := a
	aChanged.Source = []byte("package p\nvar x = 2\n")
	base := ephemeralEditDigest(dir, []fileReplacement{a})
	if base == "" || base != ephemeralEditDigest(dir, []fileReplacement{a}) {
		t.Fatalf("digest not deterministic: %q", base)
	}
	if ephemeralEditDigest(dir, []fileReplacement{aChanged}) == base {
		t.Fatal("distinct content shares an identity")
	}
	if ephemeralEditDigest(dir, []fileReplacement{b}) == base {
		t.Fatal("same content in a different file shares an identity")
	}
	if ephemeralEditDigest(dir, []fileReplacement{a, b}) == base {
		t.Fatal("a wider replacement set shares a narrower set's identity")
	}
}

// A cancelled write refuses at entry: an uncontended flock is granted
// without consulting ctx, so the guard is the write path's own
// (REQ-result-ephemeral-attest).
func TestRecordEphemeralAttestationRefusesCancelled(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	att := EphemeralAttestation{EditDigest: "d", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "why"}
	if err := RecordEphemeralAttestation(cancelled, path, att); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled record left a file: %v", err)
	}
}
