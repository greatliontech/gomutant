package gomutant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/posturemod"
)

// A run states every completed record's reuse posture beside its
// measurement, judged over the views the run built: a target whose
// package graph shares mutated dynamic state (a function-valued
// package variable stored through after initialization) measures —
// its kills are real — and is not reusable, the freshness channel
// naming the variable and the stored observation (the computed call
// through it) beside it; a clean target, whose oracle spans a second
// package so the run builds both postures' sets, is current with no
// channel — and every judgment reads the run's own set for the
// record's posture. No second operation
// is needed to learn either (REQ-result-run-posture).
//
//gofresh:pure
func TestRunStatesEachRecordsReusePosture(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := posturemod.Write(t)
	// The run's own views serve every judgment: a supplementary build
	// during the posture pass (the judgment's own fallback, which the
	// hook observes; the derived-oracle delta's route is unreachable for
	// a record this run measured) is the fault this pins against.
	priorHook := inspectionSupplementaryViewHook
	inspectionSupplementaryViewHook = func(symbols []string) { t.Errorf("the posture pass built supplementary views for %v", symbols) }
	t.Cleanup(func() { inspectionSupplementaryViewHook = priorHook })
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var postures []RecordPosture
	findings, err := tr.Run(context.Background(), []Target{
		{Symbol: "example.com/posture/shared.Read", Oracle: []string{"example.com/posture/shared.TestRead"}},
		{Symbol: "example.com/posture/clean.Add", Oracle: []string{"example.com/posture/clean.TestAdd", "example.com/posture/other.TestAddFromOutside"}},
	}, Options{Budget: 4, OracleTimeout: time.Minute, Posture: func(p RecordPosture) { postures = append(postures, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Killed == 0 {
		t.Fatalf("findings %+v", findings)
	}
	byName := map[string]RecordPosture{}
	for _, p := range postures {
		byName[p.Symbol] = p
	}
	shared, clean := byName["example.com/posture/shared.Read"], byName["example.com/posture/clean.Add"]
	if shared.Measurement != "measured" || shared.Reuse != FindingUnverifiable || len(shared.Reasons) == 0 ||
		shared.Reasons[0].Channel != PostureFreshness || !strings.Contains(shared.Reasons[0].Reason, "shares mutated dynamic state") ||
		!strings.Contains(shared.Reasons[0].Reason, "hook") || shared.Analysis != AnalysisSource ||
		len(shared.Reasons) != 2 || shared.Reasons[1].Channel != PostureStoredObservation || !strings.Contains(shared.Reasons[1].Reason, "computed function call") {
		t.Fatalf("shared posture = %+v", shared)
	}
	if clean.Measurement != "measured" || clean.Reuse != FindingCurrent || len(clean.Reasons) != 0 || clean.Analysis != "" {
		t.Fatalf("clean posture = %+v", clean)
	}
	summary := SummarizeRun(findings, tr.Selection())
	summary.AddPostures(byName)
	if summary.Reusable != 1 || len(summary.NotReusable) != 1 || summary.NotReusable[0].Symbol != shared.Symbol || summary.OmittedNotReusable != 0 {
		t.Fatalf("summary posture = %+v", summary)
	}
}

// The not-reusable roster is capped with the remainder counted, in
// symbol order, and a skipped record is neither reusable nor listed.
//
//gofresh:pure
func TestSummaryPostureRosterIsCappedAndOrdered(t *testing.T) {
	postures := map[string]RecordPosture{}
	for i := 0; i < PostureCap+3; i++ {
		symbol := string(rune('z'-i)) + ".F"
		postures[symbol] = RecordPosture{Symbol: symbol, Measurement: "measured", Reuse: FindingStale}
	}
	postures["a.Skipped"] = RecordPosture{Symbol: "a.Skipped", Measurement: "skipped"}
	postures["a.Fresh"] = RecordPosture{Symbol: "a.Fresh", Measurement: "cached", Reuse: FindingCurrent}
	var s RunSummary
	s.AddPostures(postures)
	if s.Reusable != 1 || len(s.NotReusable) != PostureCap || s.OmittedNotReusable != 3 {
		t.Fatalf("summary = reusable %d, listed %d, omitted %d", s.Reusable, len(s.NotReusable), s.OmittedNotReusable)
	}
	for i := 1; i < len(s.NotReusable); i++ {
		if s.NotReusable[i-1].Symbol >= s.NotReusable[i].Symbol {
			t.Fatalf("roster out of symbol order at %d: %s, %s", i, s.NotReusable[i-1].Symbol, s.NotReusable[i].Symbol)
		}
	}
}

// Every channel and analysis the posture composes, over inspections
// shaped as the checks produce them: the deciding check's channel
// carries the reason; flagged candidates add theirs beside a freshness
// refusal and are the channel when they decided; runtime inputs name
// themselves; a failed judgment is its own analysis; the stored
// observation rides a refused reuse only, and never repeats the
// judgment's text.
func TestPostureComposesEveryChannel(t *testing.T) {
	flagged := []CandidateEvidence{{Position: "p:1:1", Operator: "x", Reason: "log incomplete", Disposition: "killed"}}
	observed := Finding{TargetEvidence: SubjectEvidence{ObservationObservable: false, ObservationReason: "subject reachability is not closed"}}
	cases := []struct {
		name       string
		f          Finding
		inspection FindingInspection
		err        error
		channels   []string
		analysis   string
		reuse      FindingState
	}{
		{"freshness with flagged candidates composes", Finding{CandidateEvidence: flagged},
			FindingInspection{State: FindingUnverifiable, Reason: "target: package graph shares mutated dynamic state: x", CandidateEvidence: flagged}, nil,
			[]string{PostureFreshness, PostureCandidateEvidence}, AnalysisSource, FindingUnverifiable},
		{"candidate evidence decided", Finding{CandidateEvidence: flagged},
			withCandidateEvidence(FindingInspection{State: FindingCurrent}, Finding{CandidateEvidence: flagged}), nil,
			[]string{PostureCandidateEvidence}, AnalysisReexecute, FindingUnverifiable},
		{"runtime inputs", Finding{},
			FindingInspection{State: FindingUnverifiable, Reason: "manifest unreadable", Channel: PostureRuntimeInputs}, nil,
			[]string{PostureRuntimeInputs}, AnalysisReexecute, FindingUnverifiable},
		{"stale re-measures", Finding{}, FindingInspection{State: FindingStale, Reason: "body moved"}, nil,
			[]string{PostureFreshness}, AnalysisRemeasure, FindingStale},
		{"judgment failed keeps the stored observation", observed, FindingInspection{}, errors.New("tree: load failed"),
			[]string{PostureFreshness, PostureStoredObservation}, AnalysisJudge, FindingUnverifiable},
		{"stored observation beside a refusal", observed,
			FindingInspection{State: FindingUnverifiable, Reason: "target: shares mutated dynamic state"}, nil,
			[]string{PostureFreshness, PostureStoredObservation}, AnalysisSource, FindingUnverifiable},
		{"stored observation never repeats the judgment", observed,
			FindingInspection{State: FindingUnverifiable, Reason: targetReasonPrefix + "subject reachability is not closed"}, nil,
			[]string{PostureFreshness}, AnalysisSource, FindingUnverifiable},
		{"current carries no channel", observed, FindingInspection{State: FindingCurrent}, nil, nil, "", FindingCurrent},
	}
	for _, c := range cases {
		p := posture(c.f, c.inspection, c.err)
		var got []string
		for _, r := range p.Reasons {
			got = append(got, r.Channel)
		}
		if p.Reuse != c.reuse || p.Analysis != c.analysis || strings.Join(got, ",") != strings.Join(c.channels, ",") {
			t.Fatalf("%s: posture = %+v", c.name, p)
		}
	}
}
