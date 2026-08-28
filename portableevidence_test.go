package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// A relative-era record folded back into the merge world absolutizes
// against its own module base first: in a workspace, a sub-module's
// module-relative manifest interpreted under the tree root would name
// the wrong files, and the merge would read the record as moved
// (REQ-inputs-absolute-identities, gofresh's cross-module merge
// contract).
func TestFoldRecordedUnionAbsolutizesWorkspaceRecords(t *testing.T) {
	tree, err := Load("internal/engine/testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	env := engine.OracleEvidenceEnv(tree.eng.GoEnv())
	subDir := filepath.Join(tree.dir, "sub")
	if err := os.WriteFile(filepath.Join(subDir, "data.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join(subDir, "data.txt")) })

	// The recorded evidence: a module-relative manifest anchored at the
	// SUB module — the persisted portable form this era writes.
	recorded, err := runtimeinput.FromTestLogEnv([]byte("open data.txt\n"), subDir, subDir, env, runtimeinput.WithCompletedProcess("recorded"), runtimeinput.WithBracket(testBracket(t, subDir)))
	if err != nil {
		t.Fatal(err)
	}
	recordedState, err := runtimeinput.CompletedState(recorded)
	if err != nil {
		t.Fatal(err)
	}
	rec := Finding{TargetEvidence: SubjectEvidence{Symbol: "example.com/ws/sub.F", ModuleBase: "sub", RuntimeInputs: recordedState.Manifest, RuntimeDigest: recordedState.Digest}}

	// The fresh union at the tree root, already absolute (the engine's
	// in-memory form).
	fresh, err := runtimeinput.FromTestLogEnv(nil, tree.dir, tree.dir, env, runtimeinput.WithCompletedProcess("fresh"), runtimeinput.WithBracket(testBracket(t, tree.dir)))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = runtimeinput.AbsoluteEnv(fresh, tree.dir, env)
	if err != nil {
		t.Fatal(err)
	}

	folded, err := tree.foldRecordedUnion(ctx, tree.eng.GoEnv(), rec, subDir, fresh)
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.CompletedState(folded)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("workspace fold degraded to unverifiable: %+v", state)
	}
	paths, err := runtimeinput.Paths(state.Manifest, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(subDir, "data.txt")
	found := false
	for _, p := range paths {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("folded union lost the sub-module identity %s: %v", want, paths)
	}
}

// A relative-era record's unchanged union compares undiverged at the
// splice: the divergence judgment reads the record in its own
// persisted form, so a routine serve extension never false-diverges
// into a non-reusable stamp (REQ-result-stale's re-stamp section).
func TestApplySplicedUnionAcceptsRelativeEraRecords(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":   "module example.com/splice\n\ngo 1.26.4\n",
		"p.go":     "package splice\n",
		"data.txt": "x",
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
	rel, err := runtimeinput.FromTestLogEnv([]byte("open data.txt\n"), root, root, env, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	relState, err := runtimeinput.CompletedState(rel)
	if err != nil {
		t.Fatal(err)
	}
	// The in-memory union is the absolute form of the SAME content.
	union, err := runtimeinput.AbsoluteEnv(rel, root, env)
	if err != nil {
		t.Fatal(err)
	}
	evidence := SubjectEvidence{Symbol: "example.com/splice.F", RuntimeInputs: relState.Manifest, RuntimeDigest: relState.Digest}
	rec := Finding{TargetEvidence: evidence, OracleEvidence: []SubjectEvidence{evidence}}
	_, same, err := tree.applySplicedUnion(context.Background(), env, rec, union, newPortableUnion(union, engine.OracleEvidenceEnv(env)), root)
	if err != nil {
		t.Fatal(err)
	}
	if same.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("relative-era record false-diverged: %+v", same.TargetEvidence)
	}
	if same.TargetEvidence.RuntimeInputs != relState.Manifest {
		t.Fatalf("undiverged splice rewrote the recorded manifest: %+v", same.TargetEvidence)
	}
}

// mergeScoredFacts is the one entry every assembly arm joins a
// window's ceiling facts through: a memory-decided verdict joins the
// record's fact, and the pin raises to the larger effective ceiling —
// the directional serve's premise is that the recorded pin bounds
// every verdict's measurement ceiling (REQ-exec-oracle-memory,
// REQ-result-stale).
func TestMergeScoredFactsJoinsCeilingFacts(t *testing.T) {
	decided := windowScores{memoryDecided: []bool{false, true}}
	clean := windowScores{memoryDecided: []bool{false, false}}
	cases := []struct {
		name        string
		rec         Finding
		scores      windowScores
		currentPin  int64
		wantDecided bool
		wantPin     int64
	}{
		{"fresh clean keeps pin", Finding{OracleMemoryBytes: 1 << 30}, clean, 1 << 30, false, 1 << 30},
		{"memory-decided joins", Finding{OracleMemoryBytes: 1 << 30}, decided, 1 << 30, true, 1 << 30},
		{"recorded fact carries", Finding{OracleMemoryBytes: 1 << 30, OracleCeilingDecided: true}, clean, 1 << 30, true, 1 << 30},
		{"larger current raises the pin", Finding{OracleMemoryBytes: 1 << 30}, clean, 2 << 30, false, 2 << 30},
		{"unlimited current raises to unlimited", Finding{OracleMemoryBytes: 1 << 30}, clean, 0, false, 0},
		{"smaller current keeps the recorded bound", Finding{OracleMemoryBytes: 2 << 30}, clean, 1 << 30, false, 2 << 30},
		{"unlimited record stays unlimited", Finding{OracleMemoryBytes: 0}, clean, 1 << 30, false, 0},
	}
	for _, tc := range cases {
		got := mergeScoredFacts(tc.rec, tc.scores, tc.currentPin)
		if got.OracleCeilingDecided != tc.wantDecided || got.OracleMemoryBytes != tc.wantPin {
			t.Errorf("%s: decided=%v pin=%d, want decided=%v pin=%d", tc.name, got.OracleCeilingDecided, got.OracleMemoryBytes, tc.wantDecided, tc.wantPin)
		}
	}
}
