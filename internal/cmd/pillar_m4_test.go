package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
)

func TestPillarM4ReporterPreservesCandidateCountersAcrossAuditEvents(t *testing.T) {
	var buf bytes.Buffer
	rep := newRunReporter(&buf, false, 2)
	rep.decision(gomutant.RunDecision{Symbol: "example.com/p.F", Action: "measure", Candidates: 5})
	rep.executing(gomutant.ExecutionEvent{
		Phase:           "tick",
		Symbol:          "example.com/p.F",
		CandidatesDone:  5,
		CandidatesTotal: 5,
	})
	// Non-candidate event (e.g. post-candidate audit) arrives with zero candidate totals.
	rep.executing(gomutant.ExecutionEvent{
		Phase:  "auditing",
		Symbol: "example.com/p.F",
	})
	snap := rep.progressSnapshot()
	if snap.CandidatesDone != 5 || snap.CandidatesTotal != 5 {
		t.Fatalf("progressSnapshot candidates = %d/%d, want 5/5 preserved across audit event", snap.CandidatesDone, snap.CandidatesTotal)
	}
}

func TestPillarM4ExplainCommandTriageAndSymbolArms(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":     "module example.com/expmod\n\ngo 1.24\n",
		"lib.go":     "package expmod\n\nfunc Value() int { return 1 }\n",
		"lib_test.go": "package expmod\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ev := func(sym string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{
			Symbol: sym,
			Fingerprint: gofresh.Fingerprint{
				MaximalClosure:       "c",
				TestVariantClosure:   "tv",
				ObservationAssertion: "caller assertion",
				RuntimeInputs:        "eyJ2IjoxfQ",
				RuntimeDigest:        "d",
				Guards:               guard.Guards{Toolchain: "go", BuildConfig: "b"},
				ObservationProof:     gofresh.ObservationProof{Strategy: "s", Subject: gofresh.Subject{Package: "example.com/expmod", Symbol: "Value"}, Observable: true, Evidence: "e"},
				ResultKind:           gofresh.CodeResult,
			},
		}
	}
	docPath := gomutant.FindingsPathAt(dir, "")
	store, err := gomutant.OpenStore(docPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(t.Context(), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{{
			Symbol:                "example.com/expmod.Value",
			BodyHash:              "h",
			OperatorSet:           "go/2",
			OracleTimeout:         (30 * time.Second).String(),
			OracleExecutionPolicy: gomutant.FullOracleExecutionPolicy,
			Dirty:                 true,
			EvidenceFault:         "external file input: /etc/hosts",
			TargetEvidence:        ev("example.com/expmod.Value"),
			OracleEvidence:        []gomutant.SubjectEvidence{ev("example.com/expmod.TestValue")},
			CandidateCount:        1,
			Generated:             1,
			Mutants:               1,
			Operators:             []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
			Survivors:             []gomutant.Survivor{{Position: "lib.go:3:21", Operator: "zero return", Execution: "executed-and-passed"}},
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Triage arm (human + JSON)
	var out bytes.Buffer
	if err := explainCommand(t.Context(), explainOptions{dir: dir, findingsFile: defaultFindings}, &out); err != nil {
		t.Fatalf("explain triage (human): %v", err)
	}
	if !strings.Contains(out.String(), "0 repo-committable, 1 machine-local only") || !strings.Contains(out.String(), "external file input: /etc/hosts") {
		t.Fatalf("unexpected human triage output:\n%s", out.String())
	}

	out.Reset()
	if err := explainCommand(t.Context(), explainOptions{dir: dir, findingsFile: defaultFindings, json: true}, &out); err != nil {
		t.Fatalf("explain triage (json): %v", err)
	}
	var triage explainView
	if err := json.Unmarshal(out.Bytes(), &triage); err != nil {
		t.Fatalf("unmarshal triage json: %v", err)
	}
	if triage.LocalOnly == nil || *triage.LocalOnly != 1 || len(triage.Promotion) == 0 {
		t.Fatalf("unexpected triage json: %+v", triage)
	}

	// 2. Single-symbol arm (JSON)
	out.Reset()
	if err := explainCommand(t.Context(), explainOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/expmod.Value", json: true}, &out); err != nil {
		t.Fatalf("explain symbol (json): %v", err)
	}
	var symView explainView
	if err := json.Unmarshal(out.Bytes(), &symView); err != nil {
		t.Fatalf("unmarshal symbol json: %v", err)
	}
	if symView.Layer != gomutant.LayerLocal || len(symView.LayerReasons) == 0 || len(symView.Open) != 1 {
		t.Fatalf("unexpected symbol view: %+v", symView)
	}

	// 3. Mutually exclusive --symbol and --label refusal
	if err := explainCommand(t.Context(), explainOptions{dir: dir, symbol: "a", label: "b"}, &out); err == nil || !strings.Contains(err.Error(), "--symbol or --label, not both") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}
