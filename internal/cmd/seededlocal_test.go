package cmd

import (
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gomutant"
)

// seededLocalFinding is a complete machine-local (dirty) record.
func seededLocalFinding(symbol string) gomutant.Finding {
	evidence := func(sym string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: sym, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: sym}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
	}
	return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence(symbol),
		OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")},
		Operators:      []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors:      []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}}
}
