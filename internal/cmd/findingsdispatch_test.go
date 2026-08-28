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
		TargetEvidence: gomutant.SubjectEvidence{Symbol: "example.com/empty.Gone", MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: "example.com/empty.Gone", ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"},
		OracleEvidence: []gomutant.SubjectEvidence{{Symbol: "example.com/empty.TestGone", MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: "example.com/empty.TestGone", ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}},
		Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors: []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}}
	if err := gomutant.UpdateDocument(findingsAt(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
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
	if !strings.Contains(filtered.String(), "no findings") {
		t.Fatalf("state filter kept a detached record: %q", filtered.String())
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
	var bySymbol bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.Other"}, &bySymbol); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bySymbol.String(), "no findings") {
		t.Fatalf("symbol filter kept a foreign record: %q", bySymbol.String())
	}
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, state: "bogus"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown state accepted")
	}
}
