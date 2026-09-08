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
	first := EphemeralAttestation{EditDigest: "bbb", RawEditDigest: "bbb-raw", Files: []string{"a.go"}, TestPkg: "example.com/p", Run: "^TestA$", Reason: "defense-in-depth nil guard"}
	second := EphemeralAttestation{EditDigest: "aaa", RawEditDigest: "aaa-raw", Files: []string{"b.go"}, TestPkg: "example.com/p", Run: "^TestB$", Reason: "union-vs-dispatch equivalence"}
	if err := RecordEphemeralAttestation(context.Background(), path, first, false); err != nil {
		t.Fatal(err)
	}
	if err := RecordEphemeralAttestation(context.Background(), path, second, false); err != nil {
		t.Fatal(err)
	}
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[0].EditDigest != "aaa" || atts[1].EditDigest != "bbb" {
		t.Fatalf("record = %+v, want two digest-sorted entries", atts)
	}
	// A re-judgment is the explicit replacement; without asking for
	// it the standing row is named and kept.
	rejudged := first
	rejudged.Reason = "re-judged: still equivalent, sharper ground"
	if err := RecordEphemeralAttestation(context.Background(), path, rejudged, false); err == nil {
		t.Fatal("a second attestation of a standing digest must refuse unless replacement is asked for")
	}
	if err := RecordEphemeralAttestation(context.Background(), path, rejudged, true); err != nil {
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
	if err := os.WriteFile(path, []byte(`{"version":3,"attestations":[]}`), 0o644); err != nil {
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
	base := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1", RawEditDigest: "r1"}
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
	res, err := tr.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: 0, Runs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed || len(res.UnexercisedFiles) != 0 || res.EditDigest == "" {
		t.Fatalf("probe = %+v, want an exercised survivor carrying its digest", res)
	}
	again, err := tr.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: 0, Runs: 1})
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
	if err := RecordEphemeralAttestation(context.Background(), path, att, false); err != nil {
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
	unknown := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1", RawEditDigest: "r1", CoverageUnknown: true}
	if _, err := AttestEphemeralEquivalence(context.Background(), t.TempDir(), &unknown, "why"); err == nil || !strings.Contains(err.Error(), "exercise state is unknown") {
		t.Fatalf("unknown-coverage probe attested: %v", err)
	}
}

// The write validates with the load's own predicate: an invalid row
// can never land as a poison pill that makes the record unloadable
// (REQ-result-ephemeral-attest).
func TestRecordEphemeralAttestationRefusesInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	bad := EphemeralAttestation{EditDigest: "d", RawEditDigest: "r", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$"}
	if err := RecordEphemeralAttestation(context.Background(), path, bad, false); err == nil || !strings.Contains(err.Error(), "needs editDigest, files, testPkg, run, and reason") {
		t.Fatalf("reason-free row written: %v", err)
	}
	// A canonical row carries its raw digest as its second key; one
	// without it refuses at the write and at the load alike.
	rawless := EphemeralAttestation{EditDigest: "d", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "why"}
	if err := RecordEphemeralAttestation(context.Background(), path, rawless, false); err == nil || !strings.Contains(err.Error(), "needs rawEditDigest") {
		t.Fatalf("raw-less canonical row written: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused write left a record file: %v", err)
	}
	planted := `{"version":2,"attestations":[{"editDigest":"d","files":["f.go"],"testPkg":"p","run":"^T$","reason":"why"}]}`
	if err := os.WriteFile(path, []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEphemeralAttestations(path); err == nil || !strings.Contains(err.Error(), "needs rawEditDigest") {
		t.Fatalf("raw-less canonical row loaded: %v", err)
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
	res := EphemeralResult{Files: []string{"f.go"}, TestPkg: "example.com/p", Run: "^T$", Runs: 1, EditDigest: "d1", RawEditDigest: "r1"}
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
	base := ephemeralEditDigest(dir, []fileReplacement{a}, true)
	if base == "" || base != ephemeralEditDigest(dir, []fileReplacement{a}, true) {
		t.Fatalf("digest not deterministic: %q", base)
	}
	if ephemeralEditDigest(dir, []fileReplacement{aChanged}, true) == base {
		t.Fatal("distinct content shares an identity")
	}
	if ephemeralEditDigest(dir, []fileReplacement{b}, true) == base {
		t.Fatal("same content in a different file shares an identity")
	}
	if ephemeralEditDigest(dir, []fileReplacement{a, b}, true) == base {
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
	att := EphemeralAttestation{EditDigest: "d", RawEditDigest: "r", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "why"}
	if err := RecordEphemeralAttestation(cancelled, path, att, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled record left a file: %v", err)
	}
}

// Two spellings of one mutant differing in formatting alone share the
// canonical digest while their raw digests differ; a body that does not
// parse keeps its raw form (REQ-result-ephemeral-attest).
func TestEphemeralEditDigestCanonicalFormUnifiesFormatting(t *testing.T) {
	dir := t.TempDir()
	// gofmt's own rendering is the rule: spacing, indentation,
	// alignment, blank-line runs, and import order all fold; a comment
	// stays content.
	one := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nfunc F() int {\n\treturn 1\n}\n")}}
	two := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nfunc F() int {\n\treturn 1 // same\n}\n")}}
	three := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nfunc   F()   int {\n    return   1\n}\n")}}
	oneBlank := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nfunc F() int {\n\n\treturn 1\n}\n")}}
	blankRuns := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\n\n\nfunc F() int {\n\n\n\treturn 1\n}\n")}}
	if ephemeralEditDigest(dir, one, true) == ephemeralEditDigest(dir, two, true) {
		t.Fatal("a comment is content: two different files, one digest")
	}
	if ephemeralEditDigest(dir, one, true) != ephemeralEditDigest(dir, three, true) {
		t.Fatal("formatting alone changed the canonical digest")
	}
	// gofmt folds a run of blank lines to one and keeps the one: a
	// single blank line is content, its repetition is not.
	if ephemeralEditDigest(dir, oneBlank, true) != ephemeralEditDigest(dir, blankRuns, true) {
		t.Fatal("a run of blank lines alone changed the canonical digest")
	}
	if ephemeralEditDigest(dir, one, true) == ephemeralEditDigest(dir, oneBlank, true) {
		t.Fatal("a blank line gofmt keeps must change the canonical digest")
	}
	if ephemeralEditDigest(dir, one, false) == ephemeralEditDigest(dir, three, false) || ephemeralEditDigest(dir, oneBlank, false) == ephemeralEditDigest(dir, blankRuns, false) {
		t.Fatal("the raw digests must still differ")
	}
	sorted := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc F() { fmt.Println(os.Args) }\n")}}
	unsorted := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nimport (\n\t\"os\"\n\t\"fmt\"\n)\n\nfunc F() { fmt.Println(os.Args) }\n")}}
	if ephemeralEditDigest(dir, sorted, true) != ephemeralEditDigest(dir, unsorted, true) {
		t.Fatal("import order alone changed the canonical digest")
	}
	if ephemeralEditDigest(dir, sorted, false) == ephemeralEditDigest(dir, unsorted, false) {
		t.Fatal("the raw digests of two import orders must differ")
	}
	broken := []fileReplacement{{File: "lib/lib.go", Abs: filepath.Join(dir, "lib/lib.go"), Source: []byte("package lib\n\nfunc F( {\n")}}
	if ephemeralEditDigest(dir, broken, true) != ephemeralEditDigest(dir, broken, false) {
		t.Fatal("unparseable bytes must digest in their raw form")
	}
}

// A row answers to both of its keys: a canonical row whose canonical
// digest moved (another toolchain's gofmt) still matches by the raw
// digest, a raw-keyed row matches only its raw digest, and where a
// canonical row and a raw-keyed row both name one probe the canonical
// row is the standing one, whatever the record's sort order
// (REQ-result-ephemeral-attest).
func TestEphemeralAttestationKeysAndPrecedence(t *testing.T) {
	canonical := EphemeralAttestation{EditDigest: "C1", RawEditDigest: "X", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "canonical judgment"}
	if !canonical.matchesDigests("C2", "X") {
		t.Fatal("a moved canonical form must still match by the raw key")
	}
	if canonical.matchesDigests("C2", "Y") {
		t.Fatal("neither key matches, yet the row matched")
	}
	if !canonical.matchesDigests("C1", "Y") {
		t.Fatal("the canonical key alone must match")
	}
	legacy := EphemeralAttestation{EditDigest: "X", DigestForm: digestFormRaw, Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "legacy judgment"}
	if !legacy.matchesDigests("C9", "X") || legacy.matchesDigests("X", "Z") {
		t.Fatal("a raw-keyed row answers to its raw key only")
	}
	// A canonical row for another spelling (raw X2) beside the legacy
	// row (raw X): a probe of the original spelling matches both, and
	// the canonical row stands whichever sorts first.
	other := EphemeralAttestation{EditDigest: "C1", RawEditDigest: "X2", Files: []string{"f.go"}, TestPkg: "p", Run: "^T$", Reason: "canonical judgment"}
	for _, order := range [][]EphemeralAttestation{{legacy, other}, {other, legacy}} {
		if got := standingAttestation(order, "C1", "X"); got == nil || got.Reason != "canonical judgment" {
			t.Fatalf("standing row = %+v, want the canonical row over the raw-keyed one", got)
		}
	}
	if got := standingAttestation([]EphemeralAttestation{legacy}, "C1", "X"); got == nil || got.Reason != "legacy judgment" {
		t.Fatalf("standing row = %+v, want the raw-keyed row when it is the only match", got)
	}
	if got := standingAttestation([]EphemeralAttestation{legacy, other}, "C7", "X7"); got != nil {
		t.Fatalf("standing row = %+v, want none", got)
	}
}

// A version-1 record loads with its rows marked raw and matched by the
// raw digest; the next write carries them forward under version 2; a
// second attestation of a standing digest refuses unless replacement is
// asked for (REQ-result-ephemeral-attest).
func TestEphemeralAttestationRecordVersionOneAndReattest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ephemeral-attestations.json")
	v1 := `{"version":1,"attestations":[{"editDigest":"aaaa","files":["lib/lib.go"],"testPkg":"example.com/fixture/lib","run":"^TestWeak$","reason":"legacy row"}]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].DigestForm != digestFormRaw {
		t.Fatalf("v1 rows = %+v, want one row marked raw", atts)
	}
	res := &EphemeralResult{EditDigest: "cccc", RawEditDigest: "aaaa"}
	if !atts[0].matches(res) {
		t.Fatal("a raw-keyed row must match the raw digest")
	}
	if atts[0].matches(&EphemeralResult{EditDigest: "aaaa", RawEditDigest: "zzzz"}) {
		t.Fatal("a raw-keyed row must not match the canonical digest")
	}
	fresh := EphemeralAttestation{EditDigest: "cccc", RawEditDigest: "cccc-raw", Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", Reason: "new row"}
	if err := RecordEphemeralAttestation(context.Background(), path, fresh, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"version": 2`) || !strings.Contains(string(data), `"digestForm": "raw"`) {
		t.Fatalf("record after the write = %s; want version 2 carrying the legacy row marked raw", data)
	}
	dup := EphemeralAttestation{EditDigest: "cccc", RawEditDigest: "cccc-raw", Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", Reason: "second judgment"}
	err = RecordEphemeralAttestation(context.Background(), path, dup, false)
	var already *ErrAlreadyAttested
	if !errors.As(err, &already) || already.Digest != "cccc" || already.Reason != "new row" {
		t.Fatalf("second attestation = %v, want the standing row named", err)
	}
	if err := RecordEphemeralAttestation(context.Background(), path, dup, true); err != nil {
		t.Fatal(err)
	}
	atts, err = LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[1].Reason != "second judgment" {
		t.Fatalf("after re-attest = %+v, want the row replaced beside the legacy one", atts)
	}
	// A replacement supersedes every row naming the mutant by either
	// key: a canonical row whose raw key is the legacy row's digest
	// collapses the legacy row into itself.
	successor := EphemeralAttestation{EditDigest: "dddd", RawEditDigest: "aaaa", Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", Reason: "the legacy mutant re-judged"}
	if err := RecordEphemeralAttestation(context.Background(), path, successor, false); !errors.As(err, &already) || already.Reason != "legacy row" {
		t.Fatalf("attesting over the legacy row = %v, want the legacy row refused by its raw key", err)
	}
	if err := RecordEphemeralAttestation(context.Background(), path, successor, true); err != nil {
		t.Fatal(err)
	}
	atts, err = LoadEphemeralAttestations(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[0].Reason != "second judgment" || atts[1].Reason != "the legacy mutant re-judged" || atts[1].DigestForm != "" {
		t.Fatalf("after superseding the legacy row = %+v, want it collapsed into its canonical successor", atts)
	}
}

// A surviving probe whose digest the record beside its findings document
// carries reads as attested on the result (REQ-result-ephemeral-attest).
func TestEphemeralSurvivorCarriesItsAttestation(t *testing.T) {
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
	findings := filepath.Join(t.TempDir(), "findings.json")
	req := EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", Findings: findings}
	res, err := tr.RunEphemeral(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed || res.Attested != nil {
		t.Fatalf("first probe = %+v, want an unattested survivor", res)
	}
	att, err := AttestEphemeralEquivalence(ctx, fixtureDir, res, "untested large-x branch: known-surviving by fixture design")
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordEphemeralAttestation(ctx, EphemeralAttestationsPathFor(findings), att, false); err != nil {
		t.Fatal(err)
	}
	again, err := tr.RunEphemeral(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Attested == nil || again.Attested.EditDigest != res.EditDigest || again.Attested.Reason != att.Reason {
		t.Fatalf("second probe = %+v, want the attestation row on the survivor", again)
	}
	// A differently formatted spelling of the same mutant matches too.
	spaced := strings.Replace(mutated, "return x - 2", "return   x - 2", 1)
	third, err := tr.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(spaced), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", Findings: findings})
	if err != nil {
		t.Fatal(err)
	}
	if third.Attested == nil || third.EditDigest != res.EditDigest {
		t.Fatalf("reformatted spelling = %+v, want the same canonical identity and its attestation", third)
	}
	// With no findings document named, the tree's default record is
	// the one consulted — over a copy of the fixture, so the committed
	// tree is never written beside.
	copyDir := t.TempDir()
	if err := os.CopyFS(copyDir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	copied, err := Load(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	defaultPath := EphemeralAttestationsPathFor(filepath.Join(copyDir, DefaultFindingsPath))
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RecordEphemeralAttestation(ctx, defaultPath, att, false); err != nil {
		t.Fatal(err)
	}
	fourth, err := copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$"})
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Attested == nil || fourth.Attested.EditDigest != res.EditDigest {
		t.Fatalf("default record = %+v, want the survivor matched against the tree's default record", fourth)
	}
	// A kill is evidence against equivalence: a planted row never rides it.
	killing := strings.Replace(string(inside), "return a + b", "return a - b", 1)
	if killing == string(inside) {
		t.Fatal("fixture edit failed")
	}
	killRes, err := copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(killing), TestPkg: "example.com/fixture/lib", Run: "^TestAdd$"})
	if err != nil {
		t.Fatal(err)
	}
	killAtt := EphemeralAttestation{EditDigest: killRes.EditDigest, RawEditDigest: killRes.RawEditDigest, Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", Reason: "a row planted against a killed mutant"}
	if err := RecordEphemeralAttestation(ctx, defaultPath, killAtt, false); err != nil {
		t.Fatal(err)
	}
	again2, err := copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(killing), TestPkg: "example.com/fixture/lib", Run: "^TestAdd$"})
	if err != nil {
		t.Fatal(err)
	}
	if !again2.Killed || again2.Attested != nil {
		t.Fatalf("killed probe = %+v, want the kill with no attestation row", again2)
	}
	// A standing row refuses an attestation before measurement unless
	// the replacement is asked for by name — and before the loaded-set
	// judgments: the same request naming no loaded test package is
	// refused for the standing row, never for the package.
	for _, testPkg := range []string{"example.com/fixture/lib", "example.com/fixture/nosuchpkg"} {
		_, err = copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: testPkg, Run: "^TestWeak$", RefuseAttested: true})
		var already *ErrAlreadyAttested
		if !errors.As(err, &already) || already.Digest == "" {
			t.Fatalf("refuse-attested probe under %s = %v, want the standing row refused", testPkg, err)
		}
	}
	// The request's whole shape is judged before the record is read: an
	// out-of-range runs count refuses as itself, never as the standing row.
	if _, err := copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", RefuseAttested: true, Runs: 99}); err == nil || !strings.Contains(err.Error(), "runs") || errors.As(err, new(*ErrAlreadyAttested)) {
		t.Fatalf("refuse-attested probe with runs 99 = %v, want the runs refusal ahead of the record", err)
	}
	// An unloadable record refuses every probe, attesting or not.
	if err := os.WriteFile(defaultPath, []byte(`{"version":3,"attestations":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := copied.RunEphemeral(ctx, EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$"}); err == nil || !strings.Contains(err.Error(), "version 3") {
		t.Fatalf("probe over an unloadable record = %v, want the record's refusal", err)
	}
}
