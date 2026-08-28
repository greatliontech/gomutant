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

// Every judged-question input implies judging - each term
// individually, so no input can silently no-op on the recorded path
// (REQ-result-inspection).
func TestFindingsOptionsJudgedInputs(t *testing.T) {
	cases := []struct {
		name string
		opts findingsOptions
		want bool
	}{
		{"none", findingsOptions{}, false},
		{"filters alone stay recorded", findingsOptions{symbol: "p.A", label: "l", detail: true}, false},
		{"explicit judge", findingsOptions{judge: true}, true},
		{"state filter", findingsOptions{state: "current"}, true},
		{"tags selection", findingsOptions{tags: []string{"integration"}}, true},
		{"toolchain selection", findingsOptions{toolchain: "go1.26.4"}, true},
		{"vouch", findingsOptions{vouches: []string{"example.com/x:V"}}, true},
	}
	for _, tc := range cases {
		if got := tc.opts.judged(); got != tc.want {
			t.Errorf("%s: judged() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The default inspection reads recorded facts and loads no tree: it
// answers in a directory whose module is broken, where the judged
// question refuses because the tree cannot load
// (REQ-result-inspection).
func TestFindingsDefaultNeedsNoTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module ???broken\n\ngo 1.26.4\nnot a directive\n"), 0o644); err != nil {
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
		t.Fatalf("recorded default needed a tree: %v", err)
	}
	if !strings.Contains(summary.String(), "recorded  example.com/empty.Gone") {
		t.Fatalf("recorded row missing: %q", summary.String())
	}

	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, judge: true}, &bytes.Buffer{}); err == nil {
		t.Fatal("judged inspection succeeded without a tree")
	}

	// A vouch exists only to shape the freshness derivation, so it
	// implies judging - it must reach the tree load (here, its
	// failure) rather than silently no-oping on the recorded path.
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, vouches: []string{"example.com/x:V"}}, &bytes.Buffer{}); err == nil {
		t.Fatal("vouch input did not imply judging")
	}
}
