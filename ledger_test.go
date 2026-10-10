package gomutant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure/testvariant"
)

func bindingLedger() gofresh.TestVariantLedger {
	return gofresh.TestVariantLedger{
		BindingStrategy: testvariant.BindingStrategy,
		BaseFiles: []gofresh.TestVariantFileHeader{{File: "p.go", Bindings: &gofresh.TestVariantFileBindings{
			Package: "p", References: []string{"true"}, Imports: []gofresh.TestVariantImport{{Name: "s", Path: "strings"}},
		}}},
		FileHeaders: []gofresh.TestVariantFileHeader{{File: "p_test.go", Hash: "header", Bindings: &gofresh.TestVariantFileBindings{
			Package: "p", References: []string{"TestA", "testing"}, Imports: []gofresh.TestVariantImport{{Name: "testing", Path: "testing"}},
		}}},
		Declarations: []gofresh.TestVariantDeclaration{{File: "p_test.go", Package: "p", Kind: "func", Name: "TestA", Hash: "body"}},
	}
}

// TestCompartmentBindingsRoundTrip pins the complete persisted binding surface
// and independently owned copies (REQ-result-record).
func TestCompartmentBindingsRoundTrip(t *testing.T) {
	input := bindingLedger()
	wire := compartmentLedgerFromView(input)
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CompartmentLedger
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.ledger(); !reflect.DeepEqual(got, input) {
		t.Fatalf("roundtrip = %#v, want %#v", got, input)
	}
	input.BaseFiles[0].Bindings.References[0] = "changed"
	input.FileHeaders[0].Bindings.Imports[0].Path = "changed"
	if wire.BaseFiles[0].Bindings.References[0] != "true" || wire.FileHeaders[0].Bindings.Imports[0].Path != "testing" {
		t.Fatal("conversion aliases capture state")
	}
	view := decoded.ledger()
	view.BaseFiles[0].Bindings.Imports[0].Name = "changed"
	view.FileHeaders[0].Bindings.References[0] = "changed"
	if decoded.BaseFiles[0].Bindings.Imports[0].Name != "s" || decoded.FileHeaders[0].Bindings.References[0] != "TestA" {
		t.Fatal("conversion aliases stored state")
	}
	cloned := cloneFinding(Finding{CompartmentLedger: wire})
	cloned.CompartmentLedger.BaseFiles[0].Bindings.References[0] = "changed"
	cloned.CompartmentLedger.FileHeaders[0].Bindings.Imports[0].Name = "changed"
	if wire.BaseFiles[0].Bindings.References[0] != "true" || wire.FileHeaders[0].Bindings.Imports[0].Name != "testing" {
		t.Fatal("finding clone aliases ledger bindings")
	}
}

// TestKillerDriftRequiresPreservedBindings pins source-name shadowing and
// import rebinding as whole-remeasurement boundaries (REQ-result-stale).
func TestKillerDriftRequiresPreservedBindings(t *testing.T) {
	before := bindingLedger()
	for _, mode := range []string{"preserved", "missing", "unknown", "shadowed", "import", "base"} {
		after := compartmentLedgerFromView(before).ledger()
		prior := compartmentLedgerFromView(before).ledger()
		switch mode {
		case "missing":
			prior.BindingStrategy = ""
		case "unknown":
			prior.BindingStrategy = "unknown"
		case "shadowed":
			after.Declarations = append(after.Declarations, gofresh.TestVariantDeclaration{File: "p_test.go", Package: "p", Kind: "const", Name: "true", Hash: "new"})
		case "import":
			after.FileHeaders[0].Bindings.Imports[0].Path = "example.com/testing"
		case "base":
			after.BaseFiles[0].Bindings.References = []string{"different"}
		}
		delta := gofresh.DiffTestVariantLedgers(prior, after)
		if got := killerDriftAttributable(delta, prior, after); got != (mode == "preserved") {
			t.Errorf("%s: attributable=%v", mode, got)
		}
	}
}

// TestPartialSpliceNeverBackfillsBindingEvidence preserves a legacy record's
// absence instead of turning current analysis into historical support
// (REQ-result-record).
func TestPartialSpliceNeverBackfillsBindingEvidence(t *testing.T) {
	current := bindingLedger()
	legacy := compartmentLedgerFromView(current)
	legacy.BindingStrategy = ""
	for _, prior := range []*CompartmentLedger{nil, legacy} {
		got := splicedCompartmentLedger(prior, current)
		if !reflect.DeepEqual(got, prior) {
			t.Fatal("partial measurement backfilled historical binding evidence")
		}
	}
	complete := compartmentLedgerFromView(current)
	current.Declarations = append(current.Declarations, gofresh.TestVariantDeclaration{File: "p_test.go", Package: "p", Kind: "func", Name: "TestNew", Hash: "new"})
	got := splicedCompartmentLedger(complete, current)
	if len(got.Declarations) != 2 || got.BindingStrategy != testvariant.BindingStrategy {
		t.Fatal("preserved complete bindings failed to follow an admissible splice")
	}
}

func TestLedgerBindingArraysPreserveNilAndEmpty(t *testing.T) {
	for _, empty := range []bool{false, true} {
		input := bindingLedger()
		input.BaseFiles[0].Bindings.References = nil
		input.BaseFiles[0].Bindings.Imports = nil
		if empty {
			input.BaseFiles[0].Bindings.References = []string{}
			input.BaseFiles[0].Bindings.Imports = []gofresh.TestVariantImport{}
		}
		wire := compartmentLedgerFromView(input)
		data, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		var parsed CompartmentLedger
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input, parsed.ledger()) {
			t.Fatalf("binding arrays changed: %s", data)
		}
		wire.BaseFiles = nil
		if empty {
			wire.BaseFiles = []CompartmentFileHeader{}
		}
		data, err = json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		parsed = CompartmentLedger{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(wire.BaseFiles, parsed.BaseFiles) {
			t.Fatalf("base array changed: %s", data)
		}
	}
}

// TestServedLegacyLedgerDoesNotGainBindings pins the ordinary-serve path and
// the later growth refusal after a document round trip (REQ-result-record,
// REQ-result-stale).
func TestServedLegacyLedgerDoesNotGainBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("measures and serves a fixture mutant")
	}
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod":    "module example.com/legacyledger\n\ngo 1.27\n",
		"p.go":      "package p\nfunc Answer() int { return 7 }\n",
		"p_test.go": "package p\nimport \"testing\"\nfunc TestAnswer(t *testing.T) { if Answer()!=7 { t.Fatal(Answer()) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "example.com/legacyledger.Answer"}}
	first, err := tree.Run(ctx, targets, Options{Budget: 1})
	if err != nil || len(first) != 1 || first[0].CompartmentLedger == nil {
		t.Fatalf("first: %+v, %v", first, err)
	}
	first[0].CompartmentLedger.BindingStrategy = ""
	first[0].CompartmentLedger.BaseFiles = nil
	for i := range first[0].CompartmentLedger.FileHeaders {
		first[0].CompartmentLedger.FileHeaders[i].Bindings = nil
	}
	data, err := Export(first, nil)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	served, err := tree.Run(ctx, targets, Options{Budget: 1, Prior: prior})
	if err != nil || len(served) != 1 || !served[0].Cached {
		t.Fatalf("serve: %+v, %v", served, err)
	}
	if served[0].CompartmentLedger.BindingStrategy != "" {
		t.Fatal("ordinary serve manufactured binding evidence")
	}
	f, err := os.OpenFile(filepath.Join(dir, "p_test.go"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString("\nfunc TestAdded(t *testing.T) { _ = Answer() }\n")
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v, %v", writeErr, closeErr)
	}
	tree, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	_, err = tree.Run(ctx, targets, Options{Budget: 1, Prior: served, Decision: func(d RunDecision) { decisions = append(decisions, d) }})
	if err != nil || len(decisions) != 1 || decisions[0].Action != "measure" || strings.HasPrefix(decisions[0].Reason, "served:") {
		t.Fatalf("growth with unproven bindings: %+v, %v", decisions, err)
	}
}

// TestChangedTestBindingReachedByProductionRemeasuresTheKill covers a
// production reference that the test-declaration graph cannot traverse
// (REQ-result-stale).
func TestChangedTestBindingReachedByProductionRemeasuresTheKill(t *testing.T) {
	if testing.Short() {
		t.Skip("measures a mutation before and after a test binding changes")
	}
	for _, replacement := range []string{"", "const true = (1 == 1)\n"} {
		t.Run(replacement, func(t *testing.T) {
			dir := t.TempDir()
			const tests = "package p\nimport \"testing\"\nconst true = false\nfunc TestF(t *testing.T) { if !F(1 == 1) { t.Fatal(\"unexpected\") } }\n"
			for name, text := range map[string]string{
				"go.mod":    "module example.com/basebinding\n\ngo 1.27\n",
				"p.go":      "package p\nfunc F(x bool) bool { return true || x }\n",
				"p_test.go": tests,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			tree, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			targets := []Target{{Symbol: "example.com/basebinding.F"}}
			prior, err := tree.Run(ctx, targets, Options{})
			if err != nil || len(prior) != 1 {
				t.Fatalf("prior: %+v %v", prior, err)
			}
			const op = "logical: || -> &&"
			killed := false
			for _, k := range prior[0].Kills {
				killed = killed || k.Operator == op
			}
			if !killed {
				t.Fatalf("fixture did not kill logical mutant: %+v", prior[0])
			}
			if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(strings.Replace(tests, "const true = false\n", replacement, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			tree, err = Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			var decisions []RunDecision
			after, err := tree.Run(ctx, targets, Options{Prior: prior, Decision: func(d RunDecision) { decisions = append(decisions, d) }})
			if err != nil || len(after) != 1 {
				t.Fatalf("after: %+v %v", after, err)
			}
			survived := false
			for _, s := range after[0].Survivors {
				survived = survived || s.Operator == op
			}
			if !survived || len(decisions) != 1 || strings.HasPrefix(decisions[0].Reason, "served:") {
				t.Fatalf("production binding change retained a dead kill: decisions=%+v after=%+v", decisions, after[0])
			}
		})
	}
}
