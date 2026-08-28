package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The divergence stamp's best-effort naming: the paths present in
// exactly one of two observation unions, sorted; equal sets and
// undecodable manifests name nothing - the stamp itself never depends
// on the naming (REQ-result-inspection).
func TestManifestPathDeltaNamesTheMoverBestEffort(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"shared.txt", "moved.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	observe := func(log string) (string, string) {
		t.Helper()
		obs, err := runtimeinput.FromTestLog([]byte(log), root, root, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
		if err != nil {
			t.Fatal(err)
		}
		state, err := runtimeinput.CompletedState(obs)
		if err != nil {
			t.Fatal(err)
		}
		// The engine absolutizes every observation before it leaves; a
		// recorded finding keeps the relative form. Naming must read
		// both, so each case pins both forms.
		absObs, err := runtimeinput.Absolute(obs, root)
		if err != nil {
			t.Fatal(err)
		}
		absState, err := runtimeinput.CompletedState(absObs)
		if err != nil {
			t.Fatal(err)
		}
		return state.Manifest, absState.Manifest
	}
	shared, sharedAbs := observe("open shared.txt\n")
	extended, extendedAbs := observe("open shared.txt\nopen moved.txt\n")

	for name, pair := range map[string][2]string{
		"relative form":    {shared, extended},
		"absolutized form": {sharedAbs, extendedAbs},
		"mixed forms":      {shared, extendedAbs},
	} {
		if delta := manifestPathDelta(pair[0], pair[1], root); len(delta) != 1 || delta[0] != "moved.txt" {
			t.Fatalf("%s: delta = %v, want the mover alone", name, delta)
		}
		if delta := manifestPathDelta(pair[1], pair[0], root); len(delta) != 1 || delta[0] != "moved.txt" {
			t.Fatalf("%s: reverse delta = %v, want direction-independent naming", name, delta)
		}
	}
	if delta := manifestPathDelta(shared, sharedAbs, root); delta != nil {
		t.Fatalf("equal unions named a delta across forms: %v", delta)
	}
	if delta := manifestPathDelta("not a manifest", extended, root); delta != nil {
		t.Fatalf("undecodable manifest named a delta: %v", delta)
	}
	// A decodable manifest with no tree-local entries is a statement:
	// against it, every tree-local input in the other union is the
	// delta ("everything vanished" is precisely the mover set).
	empty, _ := observe("# nothing opened\n")
	if delta := manifestPathDelta(empty, extended, root); len(delta) != 2 {
		t.Fatalf("empty-vs-extended delta = %v, want both inputs named", delta)
	}
}

// The splice-divergence stamp names the diverging inputs in its reason:
// a served record whose re-executed union departs from the persisted one
// is preserved but never reusable, and the reason says where to look
// (REQ-result-inspection's best-effort naming over REQ-result-stale's
// fail-closed bound).
func TestApplySplicedUnionNamesDivergingInputs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/splice\n\ngo 1.26.4\n",
		"p.go":       "package splice\n",
		"shared.txt": "x",
		"moved.txt":  "x",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	env := tree.eng.GoEnv()
	observe := func(log string) (runtimeinput.Observation, string) {
		t.Helper()
		obs, err := runtimeinput.FromTestLogEnv([]byte(log), root, root, env, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
		if err != nil {
			t.Fatal(err)
		}
		abs, err := runtimeinput.AbsoluteEnv(obs, root, env)
		if err != nil {
			t.Fatal(err)
		}
		state, err := runtimeinput.CompletedState(abs)
		if err != nil {
			t.Fatal(err)
		}
		return abs, state.Manifest
	}
	union, _ := observe("open shared.txt\n")
	// Production evidence is absolutized before it is recorded; the
	// naming must survive that form.
	absUnion, err := runtimeinput.AbsoluteEnv(union, root, env)
	if err != nil {
		t.Fatal(err)
	}
	union = absUnion
	_, recorded := observe("open shared.txt\nopen moved.txt\n")

	rec := Finding{Symbol: "example.com/splice.F",
		TargetEvidence: SubjectEvidence{Symbol: "example.com/splice.F", RuntimeInputs: recorded, RuntimeDigest: "recorded-digest"},
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/splice.TestF", RuntimeInputs: recorded, RuntimeDigest: "recorded-digest"}}}
	_, stamped, err := tree.applySplicedUnion(context.Background(), env, rec, union, newPortableUnion(union, engine.OracleEvidenceEnv(env)), root)
	if err != nil {
		t.Fatal(err)
	}
	if !stamped.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("diverged splice not stamped unverifiable: %+v", stamped.TargetEvidence)
	}
	if !strings.Contains(stamped.TargetEvidence.RuntimeReason, "diverging inputs: moved.txt") {
		t.Fatalf("divergence reason does not name the mover: %q", stamped.TargetEvidence.RuntimeReason)
	}

	// The observation-merge fallback names the diverging inputs the same
	// way: a union that cannot merge for reuse says where to look.
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflicting, _ := observe("open shared.txt\nopen moved.txt\n")
	merged, err := mergeFindingObservationsContext(context.Background(), root, env, union, conflicting)
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.CompletedState(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "could not be merged") {
		t.Fatalf("conflicting union did not land incomplete: unverifiable=%v reason=%q", state.Unverifiable, state.Reason)
	}
	if !strings.Contains(state.Reason, "diverging inputs: moved.txt") {
		t.Fatalf("merge-fallback reason does not name the mover: %q", state.Reason)
	}
}

// Each completed solo probe's observed module-local input paths union
// into the attribution: the set the mutant-only diff subtracts
// (REQ-exec-oracle-guidance).
func TestProbeOracleInstabilityCollectsProbedPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a solo oracle probe")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/probe\n\ngo 1.26.4\n",
		"p.go":      "package probe\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package probe\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadFile(\"data.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F() != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
		"data.txt":  "x",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	groups := []group{{pkgs: []string{"example.com/probe"}, moduleDir: root, packageDir: root}}
	attr, err := tree.probeOracleInstability(context.Background(), []string{"example.com/probe.TestF"}, groups, Options{OracleTimeout: 2 * time.Minute}, tree.eng.GoEnv())
	if err != nil {
		t.Fatal(err)
	}
	if attr.completed != 1 {
		t.Fatalf("probe did not complete: %+v", attr)
	}
	if !attr.probedPaths["data.txt"] {
		t.Fatalf("probed input not collected: %+v", attr.probedPaths)
	}
}

// The oracle-instability guidance names the module-local inputs the
// finding observed that no completed solo probe reached - wired from
// the finding's own recorded manifest through the probe path union
// (REQ-exec-oracle-guidance).
func TestEmitOracleGuidanceNamesMutantOnlyInputs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/guide\n\ngo 1.26.4\n",
		"p.go":       "package guide\n",
		"shared.txt": "x",
		"moved.txt":  "x",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	env := tree.eng.GoEnv()
	obs, err := runtimeinput.FromTestLogEnv([]byte("open shared.txt\nopen moved.txt\n"), root, root, env, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	// Production finding evidence is absolutized; the wiring must
	// recover the module-relative form from it.
	abs, err := runtimeinput.AbsoluteEnv(obs, root, env)
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.CompletedState(abs)
	if err != nil {
		t.Fatal(err)
	}

	oracle := []string{"example.com/guide.TestB", "example.com/guide.TestA"}
	cache := map[string]oracleAttribution{
		strings.Join(slices.Sorted(slices.Values(oracle)), "\x00"): {completed: 2, probedPaths: map[string]bool{"shared.txt": true}},
	}
	unstable := Finding{TargetEvidence: SubjectEvidence{RuntimeUnverifiable: true, RuntimeReason: "diverged", RuntimeInputs: state.Manifest}}
	var got []OracleGuidance
	opts := Options{Guidance: func(g OracleGuidance) { got = append(got, g) }}
	if err := tree.emitOracleGuidance(context.Background(), unstable, work{oracle: oracle}, "example.com/guide.F", opts, nil, cache); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Suggestion, "inputs observed only under mutant execution: moved.txt") {
		t.Fatalf("guidance did not name the mutant-only input: %+v", got)
	}
	if strings.Contains(got[0].Suggestion, "shared.txt") {
		t.Fatalf("probed input misattributed as mutant-only: %q", got[0].Suggestion)
	}
}
