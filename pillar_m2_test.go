package gomutant

import (
	"context"
	"strings"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/engine"
)

func TestPillarM2EvidenceFaultSeparatedFromDirtyProvenance(t *testing.T) {
	dir := t.TempDir()
	clean := storeFinding("example.com/pkg.Target", func(f *Finding) {
		f.Dirty = false
		f.Commit = "deadbeef"
		f.EvidenceFault = "an evidence manifest names a subject with no view (example.com/pkg.Ghost)"
	})
	reasons := CommittableReasons(clean, dir, nil)
	if len(reasons) != 1 || reasons[0] != clean.EvidenceFault {
		t.Fatalf("CommittableReasons for clean finding with EvidenceFault = %v, want [%q]", reasons, clean.EvidenceFault)
	}
	for _, r := range reasons {
		if strings.Contains(r, "dirty worktree provenance") || strings.Contains(r, "no commit provenance") {
			t.Fatalf("clean finding with EvidenceFault rendered false git provenance clause: %q", r)
		}
	}

	// Record-level stampUnverifiable preserves subject-specific RuntimeReason
	// while recording the record-wide failure on EvidenceFault.
	withSubjectReason := storeFinding("example.com/pkg.Target", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "external directory input: /"
		f.TargetEvidence.RuntimeAttribution = `open "/" in "pkg"`
		f.OracleEvidence[0].RuntimeUnverifiable = false
		f.OracleEvidence[0].RuntimeReason = ""
	})
	const cutReason = "gofresh: observation analysis unavailable during validation: budget exhausted"
	stampUnverifiable(&withSubjectReason, cutReason)
	if withSubjectReason.EvidenceFault != cutReason {
		t.Fatalf("EvidenceFault = %q, want %q", withSubjectReason.EvidenceFault, cutReason)
	}
	if withSubjectReason.TargetEvidence.RuntimeReason != "external directory input: /" {
		t.Fatalf("TargetEvidence.RuntimeReason overwritten = %q, want preserved subject-specific reason", withSubjectReason.TargetEvidence.RuntimeReason)
	}
	if withSubjectReason.TargetEvidence.RuntimeAttribution != "" {
		t.Fatalf("TargetEvidence.RuntimeAttribution = %q, want cleared on record-wide stamp", withSubjectReason.TargetEvidence.RuntimeAttribution)
	}

	// A single record-level exemption on f.Symbol covers a record-wide cut across
	// all oracle subjects without requiring N+1 per-oracle exemption entries.
	cutFinding := storeFinding("example.com/pkg.Target", nil)
	stampUnverifiable(&cutFinding, cutReason)
	singleExemption := []Exemption{{
		Subject:   "example.com/pkg.Target",
		Reason:    cutReason,
		Rationale: "analysis budget cut is record-wide",
	}}
	if reasons := CommittableReasons(cutFinding, dir, singleExemption); len(reasons) != 0 {
		t.Fatalf("single record-level exemption left portable-line clauses: %v", reasons)
	}
	if unstableForBuckets(&cutFinding, singleExemption) {
		t.Fatal("single record-level exemption left finding unstable for survivor bucketing")
	}
}

func TestPillarM2PurityAssertionSurfaceAndStaleness(t *testing.T) {
	ctx := context.Background()
	tree, err := LoadContext(ctx, fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if tree.PurityAssertion() {
		t.Fatal("new tree has PurityAssertion() == true by default")
	}
	const symbol = "example.com/fixture/lib.Add"
	const oracle = "example.com/fixture/lib.TestAdd"
	tree.SetPureSubjects(symbol)
	if !tree.PurityAssertion() {
		t.Fatal("SetPureSubjects did not enable PurityAssertion()")
	}
	engines := tree.newSubjectEngines(nil, true, engine.OracleBounds{}, 0)
	views, err := tree.newStrictSubjectViews(ctx, []string{symbol, oracle}, tree.eng.PackageContextContext, engines)
	if err != nil {
		t.Fatal(err)
	}
	if got := views.bySymbol[symbol].fp.PurityAssertion; got != "caller assertion and source directive" {
		t.Fatalf("subject PurityAssertion under SetPureSubjects = %q, want \"caller assertion and source directive\"", got)
	}
	if got := views.bySymbol[oracle].fp.PurityAssertion; got != "source directive" {
		t.Fatalf("unlisted oracle PurityAssertion = %q, want \"source directive\" without caller assertion", got)
	}

	// Build a finding recorded under caller purity assertion, then withdraw
	// the assertion and verify InspectFinding reports "purity assertion changed".
	manifest := storeManifest()
	state, err := newCurrentRuntimeMemo().once(ctx, manifest, fixtureDir, views.bySymbol[symbol].env)
	if err != nil {
		t.Fatal(err)
	}
	recorded := Finding{
		Symbol:                symbol,
		BodyHash:              "h",
		OperatorSet:           views.bySymbol[symbol].fp.ClosureStrategy,
		OracleTimeout:         (5 * time.Second).String(),
		OracleExplicit:        true,
		OracleExecutionPolicy: FullOracleExecutionPolicy,
		Commit:                "abc",
		TargetEvidence: SubjectEvidence{
			Symbol: symbol,
			Fingerprint: gofresh.Fingerprint{
				MaximalClosure:  views.bySymbol[symbol].fp.MaximalClosure,
				PurityAssertion: views.bySymbol[symbol].fp.PurityAssertion,
				RuntimeInputs:   manifest,
				RuntimeDigest:   state.Digest,
				ResultKind:      gofresh.CodeResult,
			},
		},
		OracleEvidence: []SubjectEvidence{{
			Symbol: oracle,
			Fingerprint: gofresh.Fingerprint{
				MaximalClosure: views.bySymbol[oracle].fp.MaximalClosure,
				RuntimeInputs:  manifest,
				RuntimeDigest:  state.Digest,
				ResultKind:     gofresh.CodeResult,
			},
		}},
	}
	tree.SetPureSubjects()
	tree.SetPurityAssertion(false)
	plainViews, err := tree.newSubjectViews(ctx, []string{symbol, oracle}, true, engines.bounds)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := plainViews.bySymbol[symbol].inspectContext(ctx, recorded.TargetEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != FindingStale || inspection.Reason != "purity assertion changed" {
		t.Fatalf("inspectContext after withdrawing PurityAssertion = %+v, want stale \"purity assertion changed\"", inspection)
	}
	if ok, err := plainViews.bySymbol[symbol].evidencePrecheck(ctx, recorded.TargetEvidence, newCurrentRuntimeMemo().once); err != nil || ok {
		t.Fatalf("evidencePrecheck after withdrawing PurityAssertion = %v, %v, want false, nil", ok, err)
	}
}

func TestPillarM2ProcessIdentityDischargeAndCarryGate(t *testing.T) {
	dir := t.TempDir()
	// Process-identity reads (/proc/<pid>/stat) discharge through the reviewed
	// exemption channel, lifting both the portable-line clause and survivor
	// unstable-oracle bucketing.
	procReason := "volatile OS input: /proc/<pid>/stat"
	f := storeFinding("example.com/pkg.ProcReader", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = procReason
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeReason = procReason
	})
	if !unstableForBuckets(&f, nil) {
		t.Fatal("uncovered process-identity read should be unstable for buckets")
	}
	exemptions := []Exemption{{
		Subject:   "example.com/pkg.ProcReaderTest",
		Reason:    procReason,
		Rationale: "process identity read only constructs a scratch temp filename",
	}}
	stampExemptions(&f, exemptions)
	if len(f.Exempted) != 1 || unstableForBuckets(&f, exemptions) {
		t.Fatalf("exempted process-identity read: Exempted=%+v unstable=%v", f.Exempted, unstableForBuckets(&f, exemptions))
	}
	if reasons := CommittableReasons(f, dir, exemptions); len(reasons) != 0 {
		t.Fatalf("CommittableReasons after process-identity exemption = %v, want empty", reasons)
	}

	// Unshaped carry gate (mutationDomainHeld) vs shaped carry gate (sameAttestationPins).
	prior := storeFinding("example.com/pkg.F", func(f *Finding) {
		f.BodyHash = "same-body"
		f.TargetEvidence.MaximalClosure = "closure-1"
		f.TargetEvidence.ClosureStrategy = "ast/v1"
		f.Survivors = []Survivor{{Position: "f.go:10:2", Operator: "branch", Site: "x > 0"}}
		f.Attested = []Attestation{{Position: "f.go:10:2", Operator: "branch", Reason: "equivalent guard", Site: "x > 0"}}
	})
	current := storeFinding("example.com/pkg.F", func(f *Finding) {
		f.BodyHash = "same-body"
		f.TargetEvidence.MaximalClosure = "closure-2"
		f.TargetEvidence.ClosureStrategy = "ssa/v2"
		f.Survivors = []Survivor{{Position: "f.go:10:2", Operator: "branch", Site: "x > 0"}}
	})
	if !mutationDomainHeld(prior, current) {
		t.Fatal("mutationDomainHeld should hold across helper/closure moves when BodyHash and OperatorSet match")
	}
	if sameAttestationPins(prior, current) {
		t.Fatal("sameAttestationPins should refuse when MaximalClosure moved")
	}
	if move := derivationMove(prior.TargetEvidence, current.TargetEvidence); move == "" {
		t.Fatal("derivationMove should report ClosureStrategy change")
	}

	// Shaped target under stampUnverifiable keeps TargetEvidence zero so Export succeeds.
	shaped := storeFinding("example.com/pkg/manual", func(f *Finding) {
		f.OperatorSet = shapedOperatorSet
		f.TargetEvidence = SubjectEvidence{}
		f.Shape = &TargetShape{Manual: &ManualSpec{
			File:  "f.go",
			Edits: []ManualEdit{{Find: "return 1", Replace: "return 0"}},
		}}
	})
	stampUnverifiable(&shaped, "gofresh: analysis budget exceeded")
	if shaped.TargetEvidence != (SubjectEvidence{}) {
		t.Fatalf("shaped finding TargetEvidence was dirtied by stampUnverifiable: %+v", shaped.TargetEvidence)
	}
	if _, err := Export([]Finding{shaped}, nil); err != nil {
		t.Fatalf("Export of shaped finding after stampUnverifiable failed: %v", err)
	}
}
