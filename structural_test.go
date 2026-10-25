package gomutant

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/stipulate/structural"
)

type taggedField struct {
	name string
	typ  reflect.Type
	tag  reflect.StructTag
}

func assertTaggedStruct[T any](t *testing.T, want []taggedField) {
	t.Helper()
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Struct || typ.NumField() != len(want) {
		t.Fatalf("%s has %d fields, want %d", typ, typ.NumField(), len(want))
	}
	for i, w := range want {
		f := typ.Field(i)
		if !f.IsExported() || f.Anonymous || f.Name != w.name || f.Type != w.typ || f.Tag != w.tag {
			t.Fatalf("%s field %d = (%s %s %q), want (%s %s %q)", typ, i, f.Name, f.Type, f.Tag, w.name, w.typ, w.tag)
		}
	}
}

func TestTargetModelShape(t *testing.T) {
	structural.FunctionSignature[func(Target) bool](t, Target.Shaped)
	structural.FunctionSignature[func([]byte) ([]Target, error)](t, ParseTargets)
	structural.FunctionSignature[func(*Tree, context.Context, SelectionRequest) (TargetSelection, error)](t, (*Tree).SelectTargets)
	assertTaggedStruct[Target](t, []taggedField{
		{"Symbol", reflect.TypeFor[string](), `json:"symbol"`},
		{"Oracle", reflect.TypeFor[[]string](), `json:"oracle,omitempty"`},
		{"Labels", reflect.TypeFor[[]string](), `json:"labels,omitempty"`},
		{"OracleExplicit", reflect.TypeFor[bool](), `json:"oracleExplicit,omitempty"`},
		{"Structural", reflect.TypeFor[*StructuralSpec](), `json:"structural,omitempty"`},
		{"Manual", reflect.TypeFor[*ManualSpec](), `json:"manual,omitempty"`},
	})
	assertTaggedStruct[StructuralSpec](t, []taggedField{
		{"Class", reflect.TypeFor[string](), `json:"class"`},
		{"Packages", reflect.TypeFor[[]string](), `json:"packages,omitempty"`},
		{"Forbidden", reflect.TypeFor[string](), `json:"forbidden,omitempty"`},
		{"Type", reflect.TypeFor[string](), `json:"type,omitempty"`},
		{"Interface", reflect.TypeFor[string](), `json:"interface,omitempty"`},
	})
	assertTaggedStruct[ManualSpec](t, []taggedField{
		{"File", reflect.TypeFor[string](), `json:"file"`},
		{"Edits", reflect.TypeFor[[]ManualEdit](), `json:"edits"`},
	})
	assertTaggedStruct[ManualEdit](t, []taggedField{
		{"Find", reflect.TypeFor[string](), `json:"find"`},
		{"Replace", reflect.TypeFor[string](), `json:"replace"`},
	})
}

func TestResultExportDocumentShape(t *testing.T) {
	structural.ExportedData[Document](t,
		structural.FieldOf[[]Finding]("Findings"),
		structural.FieldOf[[]CoverageBound]("CoverageBounds"),
	)
	structural.FunctionSignature[func([]Finding, []CoverageBound) ([]byte, error)](t, Export)
	structural.FunctionSignature[func([]byte) ([]Finding, error)](t, ParseFindings)
	structural.FunctionSignature[func([]byte) (Document, error)](t, ParseDocument)
	assertTaggedStruct[RecordKey](t, []taggedField{
		{"Symbol", reflect.TypeFor[string](), ""},
		{"Selection", reflect.TypeFor[string](), ""},
	})
	assertTaggedStruct[Finding](t, []taggedField{
		{"Symbol", reflect.TypeFor[string](), `json:"symbol"`},
		{"Selection", reflect.TypeFor[string](), `json:"selection,omitempty"`},
		{"Labels", reflect.TypeFor[[]string](), `json:"labels,omitempty"`},
		{"BodyHash", reflect.TypeFor[string](), `json:"bodyHash"`},
		{"OperatorSet", reflect.TypeFor[string](), `json:"operatorSet"`},
		{"Budget", reflect.TypeFor[int](), `json:"budget"`},
		{"Shape", reflect.TypeFor[*TargetShape](), `json:"shape,omitempty"`},
		{"TargetEvidence", reflect.TypeFor[SubjectEvidence](), `json:"targetEvidence"`},
		{"OracleEvidence", reflect.TypeFor[[]SubjectEvidence](), `json:"oracleEvidence"`},
		{"OracleExplicit", reflect.TypeFor[bool](), `json:"oracleExplicit"`},
		{"OracleTimeout", reflect.TypeFor[string](), `json:"oracleTimeout"`},
		{"OracleTimeoutDerived", reflect.TypeFor[bool](), `json:"oracleTimeoutDerived,omitempty"`},
		{"OracleMemoryBytes", reflect.TypeFor[int64](), `json:"oracleMemoryBytes,omitempty"`},
		{"OracleCeilingDecided", reflect.TypeFor[bool](), `json:"oracleCeilingDecided,omitempty"`},
		{"PropertyRegime", reflect.TypeFor[string](), `json:"propertyRegime,omitempty"`},
		{"OracleExecutionPolicy", reflect.TypeFor[string](), `json:"oracleExecutionPolicy,omitempty"`},
		{"CompartmentLedger", reflect.TypeFor[*CompartmentLedger](), `json:"compartmentLedger,omitempty"`},
		{"Commit", reflect.TypeFor[string](), `json:"commit,omitempty"`},
		{"Dirty", reflect.TypeFor[bool](), `json:"dirty"`},
		{"EvidenceFault", reflect.TypeFor[string](), `json:"evidenceFault,omitempty"`},
		{"Run", reflect.TypeFor[string](), `json:"run,omitempty"`},
		{"StagedTree", reflect.TypeFor[string](), `json:"stagedTree,omitempty"`},
		{"Exempted", reflect.TypeFor[[]Exemption](), `json:"exempted,omitempty"`},
		{"CandidateCount", reflect.TypeFor[int](), `json:"candidateCount"`},
		{"Generated", reflect.TypeFor[int](), `json:"generated"`},
		{"Mutants", reflect.TypeFor[int](), `json:"mutants"`},
		{"Killed", reflect.TypeFor[int](), `json:"killed"`},
		{"Discarded", reflect.TypeFor[int](), `json:"discarded"`},
		{"Operators", reflect.TypeFor[[]OperatorSummary](), `json:"operators"`},
		{"Kills", reflect.TypeFor[[]Kill](), `json:"kills,omitempty"`},
		{"Survivors", reflect.TypeFor[[]Survivor](), `json:"survivors,omitempty"`},
		{"Attested", reflect.TypeFor[[]Attestation](), `json:"attested,omitempty"`},
		{"Withdrawn", reflect.TypeFor[[]WithdrawnAttestation](), `json:"withdrawn,omitempty"`},
		{"CandidateEvidence", reflect.TypeFor[[]CandidateEvidence](), `json:"candidateEvidence,omitempty"`},
		{"Cached", reflect.TypeFor[bool](), `json:"-"`},
		{"Skipped", reflect.TypeFor[string](), `json:"-"`},
		{"Unreached", reflect.TypeFor[bool](), `json:"-"`},
	})
}

func FuzzKillAttributionAllOrNothing(f *testing.F) {
	f.Add(uint8(0), int8(0), "example.com/p.TestA")
	f.Add(uint8(2), int8(0), "example.com/p.TestA")
	f.Add(uint8(2), int8(-1), "example.com/p.TestA")
	f.Add(uint8(2), int8(1), "(timeout)")
	f.Add(uint8(1), int8(0), "")
	f.Fuzz(func(t *testing.T, killedRaw uint8, deltaRaw int8, killer string) {
		killed := int(killedRaw % 8)
		delta := int(deltaRaw % 4)
		ev := func(sym string) SubjectEvidence {
			return SubjectEvidence{
				Symbol: sym,
				Fingerprint: gofresh.Fingerprint{
					MaximalClosure:       "c",
					TestVariantClosure:   "tv",
					Guards:               guard.Guards{Toolchain: "go", BuildConfig: "b"},
					ObservationAssertion: "caller assertion",
					ObservationProof: gofresh.ObservationProof{
						Strategy:   gofresh.ObservationRTA,
						Subject:    gofresh.Subject{Package: "example.com/p", Symbol: "F"},
						Observable: true,
						Evidence:   "proof",
					},
					RuntimeInputs: "m",
					RuntimeDigest: "d",
					ResultKind:    gofresh.CodeResult,
				},
			}
		}
		finding := Finding{
			Symbol:         "example.com/p.F",
			BodyHash:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			OperatorSet:    "v1",
			Budget:         10,
			TargetEvidence: ev("example.com/p.F"),
			OracleEvidence: []SubjectEvidence{ev("example.com/p.TestA")},
			OracleTimeout:  "5s",
			Dirty:          true,
			CandidateCount: killed,
			Generated:      killed,
			Mutants:        killed,
			Killed:         killed,
		}
		if killed > 0 {
			finding.Operators = []OperatorSummary{{Operator: "arithmetic-flip", Generated: killed, Killed: killed}}
		} else {
			finding.Operators = []OperatorSummary{}
		}

		// Case 1: historical record with absent Kills (nil) is valid without inferring attribution.
		data, err := Export([]Finding{finding}, nil)
		if err != nil {
			t.Fatalf("Export without Kills failed: %v", err)
		}
		parsed, err := ParseFindings(data)
		if err != nil || len(parsed) != 1 || parsed[0].Kills != nil {
			t.Fatalf("ParseFindings historical Kills round-trip = %+v, %v", parsed, err)
		}

		// Case 2: explicit Kills list with count killed + delta.
		count := killed + delta
		if count < 0 {
			count = 0
		}
		kills := make([]Kill, count)
		for i := range kills {
			kills[i] = Kill{
				Position: fmt.Sprintf("f.go:%d:1", i+1),
				Operator: "arithmetic-flip",
				Killer:   killer,
			}
		}
		finding.Kills = kills
		validKiller := strings.TrimSpace(killer) != "" && !strings.ContainsAny(killer, "\x00\r\n")
		wantOK := count == killed && (count == 0 || validKiller)
		data, err = Export([]Finding{finding}, nil)
		if !wantOK {
			if err == nil {
				if _, perr := ParseFindings(data); perr == nil {
					t.Fatalf("accepted partial or unattributed kills (killed=%d, len(kills)=%d, killer=%q)", killed, count, killer)
				}
			}
			return
		}
		if err != nil {
			return
		}
		parsed, err = ParseFindings(data)
		if err != nil {
			t.Fatalf("ParseFindings rejected complete attributed kills: %v", err)
		}
		if len(parsed) != 1 || len(parsed[0].Kills) != killed {
			t.Fatalf("ParseFindings kills count = %d, want %d", len(parsed[0].Kills), killed)
		}
	})
}
