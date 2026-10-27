package gomutant

import (
	"encoding/json"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
)

func BenchmarkFindingsRoundTrip(b *testing.B) {
	target := SubjectEvidence{
		Symbol: "example.com/p.F",
		Fingerprint: gofresh.Fingerprint{
			MaximalClosure:      "0123456789abcdef0123456789abcdef",
			TestVariantClosure:  "fedcba9876543210fedcba9876543210",
			Guards:              guard.Guards{Toolchain: "go1.27.0 linux/amd64", BuildConfig: "abcdef0123456789abcdef0123456789"},
			PurityAssertion:     "source directive",
			DynamicStateVouches: "example.com/dep.Var",
			ClosureStrategy:     gofresh.ClosureStrategy,
			RuntimeInputs:       "eyJ2IjoxfQ",
			RuntimeDigest:       "11223344556677889900aabbccddeeff",
			ResultKind:          gofresh.CodeResult,
		},
	}
	oracle := target
	oracle.Symbol = "example.com/p.TestF"
	finding := Finding{
		OperatorSet:           "go/2",
		Budget:                3,
		OracleTimeout:         "1m0s",
		OracleExecutionPolicy: FullOracleExecutionPolicy,
		TargetEvidence:        target,
		OracleEvidence:        []SubjectEvidence{oracle},
		Survivors: []Survivor{
			{Position: "z.go:2:1", Operator: "branch/negate"},
			{Position: "a.go:1:1", Operator: "arith/sub"},
			{Position: "a.go:1:1", Operator: "cmp/eq"},
		},
		Attested: []Attestation{
			{Position: "a.go:1:1", Operator: "arith/sub", Reason: "equivalent"},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		raw, err := json.Marshal(target)
		if err != nil {
			b.Fatal(err)
		}
		var decoded SubjectEvidence
		if err := json.Unmarshal(raw, &decoded); err != nil {
			b.Fatal(err)
		}
		if !sameAttestationPins(finding, finding) || len(finding.Open()) != 2 {
			b.Fatal("unexpected finding state")
		}
	}
}
