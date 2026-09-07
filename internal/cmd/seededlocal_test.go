package cmd

import "github.com/greatliontech/gomutant"

// seededLocalFinding is a complete machine-local (dirty) record.
func seededLocalFinding(symbol string) gomutant.Finding {
	evidence := func(sym string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: sym, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: sym, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence(symbol),
		OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")},
		Operators:      []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors:      []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}}
}
