package gomutant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyEdits pins the edits form of an ephemeral replacement
// (REQ-exec-ephemeral): sequential exact-match application; zero matches,
// ambiguity, and empty matches are refused rather than guessed.
func TestApplyEdits(t *testing.T) {
	src := []byte("a + b\nreturn a + c\n")
	out, err := ApplyEdits(src, []Edit{{Old: "a + b", New: "a - b"}, {Old: "a + c", New: "0"}})
	if err != nil || string(out) != "a - b\nreturn 0\n" {
		t.Fatalf("ApplyEdits = %q, %v", out, err)
	}
	if _, err := ApplyEdits(src, []Edit{{Old: "nowhere", New: "x"}}); err == nil || !strings.Contains(err.Error(), "matches nothing") {
		t.Fatalf("zero-match accepted: %v", err)
	}
	if _, err := ApplyEdits([]byte("x x"), []Edit{{Old: "x", New: "y"}}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous match accepted: %v", err)
	}
	if _, err := ApplyEdits(src, []Edit{{Old: "", New: "y"}}); err == nil {
		t.Fatal("empty match accepted")
	}
	if _, err := ApplyEdits(src, nil); err == nil {
		t.Fatal("empty edit list accepted")
	}
	// A later edit may match text an earlier edit produced: sequential order
	// is the contract.
	out, err = ApplyEdits([]byte("one"), []Edit{{Old: "one", New: "two"}, {Old: "two", New: "three"}})
	if err != nil || string(out) != "three" {
		t.Fatalf("sequential application = %q, %v", out, err)
	}
}

// TestMergeFindings pins the document merge both faces share: fresh replaces
// by symbol, untouched persists.
func TestMergeFindings(t *testing.T) {
	prior := []Finding{{Symbol: "p.A", BodyHash: "1"}, {Symbol: "p.B", BodyHash: "2"}}
	fresh := []Finding{{Symbol: "p.A", BodyHash: "1b"}}
	got := MergeFindings(prior, fresh)
	bySym := map[string]string{}
	for _, f := range got {
		bySym[f.Symbol] = f.BodyHash
	}
	if len(got) != 2 || bySym["p.A"] != "1b" || bySym["p.B"] != "2" {
		t.Fatalf("merge = %+v", got)
	}
}

// TestMergeFindingsSkipNeverShadows pins the merge's own rule, distinct from
// Export's serialization rule: a skipped result measured nothing, so it must
// never overwrite a symbol's real record — its survivors and dispositions
// outlive a run whose oracle vanished.
func TestMergeFindingsSkipNeverShadows(t *testing.T) {
	prior := []Finding{{Symbol: "p.A", BodyHash: "h",
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "zero return"}},
		Attested:  []Attestation{{Position: "f.go:1:1", Operator: "zero return", Reason: "equivalent"}}}}
	got := MergeFindings(prior, []Finding{{Symbol: "p.A", Skipped: "no oracle"}})
	if len(got) != 1 || got[0].BodyHash != "h" || len(got[0].Attested) != 1 {
		t.Fatalf("a skipped result shadowed the real record: %+v", got)
	}
}

// TestUpdateDocument pins the locked read-merge-write (REQ-mcp-findings-doc):
// a disposition landing between a session's read and its write survives,
// because the merge runs against the re-read document under the lock; a lock
// held elsewhere is surfaced with its path, never silently overwritten.
func TestUpdateDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "findings.json")
	evidence := func(symbol string) SubjectEvidence {
		return SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	seed := []Finding{{Symbol: "p.A", BodyHash: "h", OperatorSet: "go/2", Timeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("p.A"), OracleEvidence: []SubjectEvidence{evidence("p.TestA")}, Mutants: 1,
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "zero return"}}}}
	if err := UpdateDocument(path, func(prior []Finding) ([]Finding, error) {
		return MergeFindings(prior, seed), nil
	}); err != nil {
		t.Fatal(err)
	}

	// A long-running session took its snapshot here; meanwhile a disposition
	// lands through its own locked update.
	if err := UpdateDocument(path, func(all []Finding) ([]Finding, error) {
		return all, all[0].Attest("f.go:1:1", "zero return", "equivalent")
	}); err != nil {
		t.Fatal(err)
	}

	// The long session writes its (stale-snapshot-independent) merge: the
	// update sees the re-read document, disposition intact.
	fresh := []Finding{{Symbol: "p.B", BodyHash: "h2", OperatorSet: "go/2", Timeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("p.B"), OracleEvidence: []SubjectEvidence{evidence("p.TestB")}, Mutants: 1, Killed: 1}}
	if err := UpdateDocument(path, func(current []Finding) ([]Finding, error) {
		for _, f := range current {
			if f.Symbol == "p.A" && len(f.Attested) != 1 {
				t.Fatal("the update saw a stale snapshot")
			}
		}
		return MergeFindings(current, fresh), nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	bySym := map[string]Finding{}
	for _, f := range got {
		bySym[f.Symbol] = f
	}
	if len(bySym["p.A"].Attested) != 1 || bySym["p.B"].Symbol == "" {
		t.Fatalf("concurrent disposition clobbered: %+v", got)
	}

	// A held lock is surfaced, never bypassed.
	if err := os.WriteFile(path+".lock", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err = UpdateDocument(path, func(p []Finding) ([]Finding, error) { return p, nil })
	if err == nil || !strings.Contains(err.Error(), ".lock") {
		t.Fatalf("held lock bypassed: %v", err)
	}
}

// TestLoadTargetsSniffs pins the producer sniffer (REQ-target-producers):
// stipulator's export and gomutant's own document both load through one
// entry point, keyed by the version field.
func TestLoadTargetsSniffs(t *testing.T) {
	st, err := LoadTargets([]byte(`{"stipulatorTargets":1,"targets":[{"symbol":"p.F","witnesses":["p.TestF"],"requirements":["R"]}]}`))
	if err != nil || len(st) != 1 || st[0].Oracle[0] != "p.TestF" || st[0].Labels[0] != "R" {
		t.Fatalf("stipulator export: %+v %v", st, err)
	}
	own, err := LoadTargets([]byte(`{"targets":[{"symbol":"p.F","oracle":["p.TestF"]}]}`))
	if err != nil || len(own) != 1 || own[0].Oracle[0] != "p.TestF" {
		t.Fatalf("own document: %+v %v", own, err)
	}
}
