package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
)

// The findings command's default is the bounded summary; --detail is
// the full-row opt-in (REQ-result-inspection).
func TestFindingsCommandDefaultsToSummaryRows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed := gomutant.Finding{Symbol: "example.com/empty.Gone", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: gomutant.SubjectEvidence{Symbol: "example.com/empty.Gone", Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: "example.com/empty.Gone"}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}},
		OracleEvidence: []gomutant.SubjectEvidence{{Symbol: "example.com/empty.TestGone", Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: "example.com/empty.TestGone"}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}},
		Operators:      []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors:      []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}}
	if err := gomutant.UpdateDocument(context.Background(), gomutant.FindingsPathAt(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{seed}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var summary bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings}, &summary); err != nil {
		t.Fatal(err)
	}
	// The default reads recorded facts only - state "recorded", no
	// tree loaded, no freshness derived (REQ-result-inspection).
	if !strings.Contains(summary.String(), "recorded  example.com/empty.Gone") || !strings.Contains(summary.String(), "1 open, 0 attested") {
		t.Fatalf("summary default missing the recorded row: %q", summary.String())
	}
	if strings.Contains(summary.String(), "detached") {
		t.Fatalf("summary default judged without being asked: %q", summary.String())
	}
	if strings.Contains(summary.String(), "survivor old.go:1:1") {
		t.Fatalf("summary default leaked detail lists: %q", summary.String())
	}
	// The recorded default names the judged opt-in at the point of
	// use (REQ-result-inspection); the judged view does not repeat it.
	if !strings.Contains(summary.String(), "--judge for freshness states") {
		t.Fatalf("recorded default missing the judged opt-in hint: %q", summary.String())
	}

	// --judge derives the freshness classification.
	var judged bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, judge: true}, &judged); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(judged.String(), "detached  example.com/empty.Gone") {
		t.Fatalf("--judge missing the judged row: %q", judged.String())
	}
	// A detached record says it is terminal and names the moves in
	// every judged view (REQ-result-inspection).
	if !strings.Contains(judged.String(), "terminal") || !strings.Contains(judged.String(), "prune") || !strings.Contains(judged.String(), "retarget") {
		t.Fatalf("detached row missing the terminal label: %q", judged.String())
	}
	if strings.Contains(judged.String(), "--judge for freshness states") {
		t.Fatalf("judged view repeats the opt-in hint: %q", judged.String())
	}

	var detail bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, detail: true}, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.String(), "survivor old.go:1:1") {
		t.Fatalf("--detail missing the survivor list: %q", detail.String())
	}

	// State and symbol filters narrow the roster; an unknown state
	// refuses.
	var filtered bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, state: "stale"}, &filtered); err != nil {
		t.Fatal(err)
	}
	// The zero-row answer names the input that emptied it, as the
	// structured face does (REQ-mcp-envelope).
	if !strings.Contains(filtered.String(), "no findings\nstate=stale matched none of the 1 finding(s) the other filters kept; drop it to list them\n") {
		t.Fatalf("state filter kept a detached record or named no emptier: %q", filtered.String())
	}
	// The JSON face names the same emptier beside its empty list.
	var filteredJSON, filteredNote bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, state: "stale", json: true, errOut: &filteredNote}, &filteredJSON); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(filteredJSON.String()) != "[]" || !strings.Contains(filteredNote.String(), "state=stale matched none of the 1 finding(s)") {
		t.Fatalf("JSON face under a state that emptied the roster = %q / note %q; want the empty list and the emptier named", filteredJSON.String(), filteredNote.String())
	}
	// A MATCHING state filter returns the judged row: the filter
	// implies judging rather than comparing against the recorded
	// state, which would silently empty every roster.
	var byState bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, state: "detached"}, &byState); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(byState.String(), "detached  example.com/empty.Gone") {
		t.Fatalf("matching state filter dropped the row: %q", byState.String())
	}
	// The layer counts are the walk's, rendered once (REQ-result-layers).
	if !strings.Contains(byState.String(), "0 repo-committable, 1 machine-local;") {
		t.Fatalf("summary counts not the walk's: %q", byState.String())
	}
	var bySymbol bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.Other"}, &bySymbol); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bySymbol.String(), "no findings\nthe symbol filter matched none of 1 recorded finding(s); drop it to list the document\n") {
		t.Fatalf("symbol filter kept a foreign record or named no emptier: %q", bySymbol.String())
	}
	// The label filter narrows the roster too, and the note names it.
	var byLabel bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, label: "REQ-nowhere"}, &byLabel); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(byLabel.String(), "no findings\nthe label filter matched none of 1 recorded finding(s); drop it to list the document\n") {
		t.Fatalf("label filter kept an unlabeled record or named no emptier: %q", byLabel.String())
	}
	// A changed ref is read at preparation: an unreachable ref refuses
	// even when the filters would empty the roster (REQ-exec-preparation).
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.Other", changed: "no-such-ref"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("an unreachable ref beside an emptying filter = %v, want the ref refused", err)
	}
	// An empty document names its emptier after the tail.
	empty := t.TempDir()
	var none bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: empty, findingsFile: defaultFindings}, &none); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none.String(), "no findings\nno findings recorded at "+gomutant.FindingsPathAt(empty, defaultFindings)+" - run measures the tree first\n") {
		t.Fatalf("empty document named no emptier: %q", none.String())
	}
	// The JSON face keeps its document; the note rides the human channel.
	var doc, notes bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: empty, findingsFile: defaultFindings, json: true, errOut: &notes}, &doc); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(doc.String()) != "[]" || !strings.HasPrefix(notes.String(), "no findings recorded at ") {
		t.Fatalf("json zero-row answer: document %q, notes %q", doc.String(), notes.String())
	}
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, state: "bogus"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown state accepted")
	}
}
