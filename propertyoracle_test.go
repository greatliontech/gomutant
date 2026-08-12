package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// The property-oracle pinning and prerequisite statements
// (REQ-exec-property-oracles): rapid packages run with draws pinned and
// reproducer files suppressed; gopter earns the caller-prerequisite
// statement; plain packages neither.
func TestPropertyOracleFlagsAndNotes(t *testing.T) {
	if flags := propertyOracleFlags(true); len(flags) != 2 || flags[0] != "-rapid.nofailfile" || flags[1] != "-rapid.seed=1" {
		t.Fatalf("rapid flags = %v", flags)
	}
	if flags := propertyOracleFlags(false); flags != nil {
		t.Fatalf("plain flags = %v", flags)
	}
	if got := engine.PropertyOracleBinFlags(); len(got) != 2 || got[1] != "-rapid.seed=1" {
		t.Fatalf("shared bin flags = %v", got)
	}
	note, ok := propertyOracleNote("p", "rapid")
	if !ok || !strings.Contains(note.Note, "draws pinned") || !strings.Contains(note.Note, "-rapid.seed=1") {
		t.Fatalf("rapid note = %+v", note)
	}
	note, ok = propertyOracleNote("p", "gopter")
	if !ok || !strings.Contains(note.Note, "cannot pin") {
		t.Fatalf("gopter note = %+v", note)
	}
	if _, ok := propertyOracleNote("p", ""); ok {
		t.Fatal("plain package earned a property note")
	}
}

// A run whose oracles carry property runtimes states each package's
// prerequisite exactly once, before the user could discover it
// mid-campaign: rapid names what the run pinned, gopter names what the
// caller must ensure.
func TestRunStatesPropertyOraclePrerequisites(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	var notes []PropertyOracleNote
	collect := func(n PropertyOracleNote) { notes = append(notes, n) }

	// Two targets sharing one rapid oracle package: the statement is
	// per package per run, never per target.
	if _, err := tr.Run(ctx, []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestPropRapidCheck"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestPropRapidCheck"}},
	}, Options{Budget: 1, PropertyOracle: collect}); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Runtime != "rapid" || notes[0].Package != "example.com/fixture/lib" || !strings.Contains(notes[0].Note, "draws pinned") {
		t.Fatalf("rapid prerequisite notes = %+v", notes)
	}

	notes = nil
	if _, err := tr.Run(ctx, []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/gopterprop.TestGopterProp"}}}, Options{Budget: 1, PropertyOracle: collect}); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Runtime != "gopter" || !strings.Contains(notes[0].Note, "cannot pin") {
		t.Fatalf("gopter prerequisite notes = %+v", notes)
	}

	// A mixed-runtime package earns every runtime's own statement: the
	// rapid pin is real AND the gopter caller-prerequisite is real -
	// one single-winner note would state something false either way.
	notes = nil
	if _, err := tr.Run(ctx, []Target{{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/mixedprop.TestMixedProp"}}}, Options{Budget: 1, PropertyOracle: collect}); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || notes[0].Runtime != "gopter" || notes[1].Runtime != "rapid" {
		t.Fatalf("mixed-runtime notes = %+v, want both statements sorted", notes)
	}
}

// The property regime is a measurement pin: a rapid-oracle record
// without the recorded regime (a pre-regime document) is refused every
// serve family and re-measures whole ("stale") - its verdicts were
// measured under draws the pinned regime never executes - while a
// record carrying the current regime serves plainly cached on an
// unchanged tree (REQ-exec-property-oracles, REQ-result-stale). The
// plain serve is load-bearing: it requires the rapid oracle subject's
// observation proof to record Observable, which rests on gofresh's
// property-harness audit (the observed test-main window, the
// flag-registration startup audit, and the harness boundary gate) -
// a regression in any of those demotes this serve to a drift
// re-measure and fails the exact-reason assertion below.
func TestPropertyRegimePinGatesServe(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	// The clean prop fixture: its rapid-oracle subject proves
	// Observable, so the record is born servable - the lib fixture's
	// ambient kitchen sink would demote this to a drift re-measure.
	targets := []Target{{Symbol: "example.com/fixture/prop.Add", Oracle: []string{"example.com/fixture/prop.TestPropRapidCheck"}}}
	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].PropertyRegime != engine.PropertyRegimeRapid {
		t.Fatalf("regime = %q, want the rapid regime recorded", first[0].PropertyRegime)
	}

	reasonOf := func(prior []Finding) string {
		reason := ""
		if _, err := tr.Run(ctx, targets, Options{Prior: prior, Decision: func(d RunDecision) { reason = d.Reason }}); err != nil {
			t.Fatal(err)
		}
		return reason
	}

	matching := reasonOf(first)
	if matching != "served: body, oracle closure, and runtime inputs unchanged" {
		t.Fatalf("matching-regime rapid record did not serve plainly cached: %q", matching)
	}

	preRegime := append([]Finding(nil), first...)
	preRegime[0].PropertyRegime = ""
	stripped := reasonOf(preRegime)
	if strings.HasPrefix(stripped, "served:") {
		t.Fatalf("pre-regime rapid record reached a serve family under the pinned regime: %q", stripped)
	}

	// The clean-fixture serve is a boundary, not the norm: the lib
	// fixture's kitchen-sink binary keeps a refusing observation proof,
	// so its rapid record must never reach the plain-cached serve -
	// drift re-measure is the designed behavior there.
	libTargets := []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestPropRapidCheck"}}}
	libFirst, err := tr.Run(ctx, libTargets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	libReason := ""
	if _, err := tr.Run(ctx, libTargets, Options{Prior: libFirst, Decision: func(d RunDecision) { libReason = d.Reason }}); err != nil {
		t.Fatal(err)
	}
	if libReason == "served: body, oracle closure, and runtime inputs unchanged" {
		t.Fatal("kitchen-sink rapid record served plainly cached: a refusing proof must demote it to a re-measure family")
	}
}

// Ephemeral probes deliver the same pinned rapid invocation as the
// campaign: the fixture's env-gated guard fails the baseline when
// either flag is missing, so a probe that reaches a verdict proves the
// delivery (REQ-exec-property-oracles).
func TestEphemeralDeliversPinnedRapidFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle")
	}
	t.Setenv("GOMUTANT_REQUIRE_RAPID_FLAG", "1")
	tr := fixtureTree(t)
	orig, err := os.ReadFile(filepath.Join(fixtureDir, "extprop", "extprop.go"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(orig), "true", "false", 1)
	if broken == string(orig) {
		t.Fatal("fixture edit failed")
	}
	res, err := tr.Ephemeral(context.Background(), "extprop/extprop.go", []byte(broken), "example.com/fixture/extprop", "^TestExtProp$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed {
		t.Fatalf("guarded rapid probe = %+v, want a verdict proving both pinned flags reached the process", res)
	}
}
