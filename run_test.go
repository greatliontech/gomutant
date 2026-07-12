package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// TestRunEndToEnd pins the orchestration against the fixture tree: a
// pinned-down body kills everything, an untested branch survives with its
// labels echoed (REQ-target-labels), a prior finding with matching pins is
// served from cache (REQ-result-stale), an attested survivor carries across
// a cached serve and a pin-matching re-measure (REQ-attest-survivor), a
// budget request beyond a capped finding re-measures, and the document
// round-trips (REQ-result-export).
func TestRunEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}, Labels: []string{"REQ-weak"}},
		{Symbol: "example.com/fixture/lib.I", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}

	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	add, weak, iface := first[0], first[1], first[2]
	if add.Cached || add.Mutants == 0 || add.Killed != add.Mutants || len(add.Survivors) != 0 {
		t.Fatalf("Add = %+v, want all mutants killed fresh", add)
	}
	if add.BodyHash == "" || add.TargetEvidence.Toolchain == "" || add.OperatorSet == "" || len(add.OracleEvidence) != 1 {
		t.Fatalf("Add pins incomplete: %+v", add)
	}
	if len(weak.Survivors) == 0 || weak.Labels[0] != "REQ-weak" {
		t.Fatalf("Weak = %+v, want survivors with labels echoed", weak)
	}
	if iface.Skipped != "not a function" {
		t.Fatalf("interface target = %+v, want skipped as not a function", iface)
	}
	if len(weak.Open()) != len(weak.Survivors) {
		t.Fatalf("open != survivors before any attestation")
	}

	// Attest one survivor; the export/parse round trip preserves it.
	s0 := weak.Survivors[0]
	if err := weak.Attest(s0.Position, s0.Operator, "equivalent by inspection"); err != nil {
		t.Fatal(err)
	}
	if err := weak.Attest("nowhere:1:1", "no-op", "x"); err == nil {
		t.Fatal("attested a mutant that is not a survivor")
	}
	if len(weak.Open()) != len(weak.Survivors)-1 {
		t.Fatal("attestation did not close the finding")
	}
	doc, err := Export([]Finding{add, weak, iface})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "example.com/fixture/lib.I") {
		t.Fatal("a skipped result was exported")
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior) != 2 {
		t.Fatalf("document findings = %d, want 2", len(prior))
	}

	// Second run under the same pins: served from cache, attestation intact.
	second, err := tr.Run(ctx, targets[:2], Options{Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	if !second[0].Cached || !second[1].Cached {
		t.Fatalf("unchanged pins re-measured: %+v %+v", second[0].Cached, second[1].Cached)
	}
	if len(second[1].Attested) != 1 || second[1].Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("attestation lost across cache: %+v", second[1].Attested)
	}

	// A moved pin re-measures instead of serving the cache, and sheds the
	// attestation: every source-evidence version's equivalences are re-judged
	// (REQ-result-stale, REQ-attest-survivor).
	tampered := append([]Finding(nil), prior...)
	for i := range tampered {
		tampered[i].TargetEvidence.MaximalClosure = "not-the-current-closure"
	}
	moved, err := tr.Run(ctx, targets[:2], Options{Prior: tampered})
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].Cached || moved[1].Cached {
		t.Fatal("a moved pin served from cache")
	}
	if len(moved[1].Attested) != 0 {
		t.Fatalf("attestation survived a pin move: %+v", moved[1].Attested)
	}

	// A capped prior finding never answers a larger request: budget 1 is
	// re-measured fresh under budget 1, then a budget-2 request re-measures
	// again rather than serving the capped record (REQ-result-stale).
	capped, err := tr.Run(ctx, targets[:1], Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if capped[0].Cached || capped[0].Budget != 1 || capped[0].Mutants+capped[0].Discarded != 1 {
		t.Fatalf("budget-1 run = %+v", capped[0])
	}
	cappedDoc, err := Export(capped)
	if err != nil {
		t.Fatal(err)
	}
	cappedPrior, err := ParseFindings(cappedDoc)
	if err != nil {
		t.Fatal(err)
	}
	wider, err := tr.Run(ctx, targets[:1], Options{Budget: 2, Prior: cappedPrior})
	if err != nil {
		t.Fatal(err)
	}
	if wider[0].Cached {
		t.Fatal("a capped finding answered a larger budget request")
	}
	// And the same capped request is served from the capped record.
	same, err := tr.Run(ctx, targets[:1], Options{Budget: 1, Prior: cappedPrior})
	if err != nil {
		t.Fatal(err)
	}
	if !same[0].Cached {
		t.Fatal("a covering capped finding was re-measured")
	}
}

// TestRunUnionsEveryProcessObservation pins REQ-exec-observation end to end:
// distinct mutants read distinct files before the oracle kills them, and both
// identities must survive in the finding-wide runtime manifest.
func TestRunUnionsEveryProcessObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	tg := Target{Symbol: "example.com/fixture/lib.PickInput", Oracle: []string{"example.com/fixture/lib.TestPickInput"}}
	findings, err := tr.Run(context.Background(), []Target{tg}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Mutants != 2 || findings[0].Killed != 2 {
		t.Fatalf("PickInput finding = %+v, want two killed mutants", findings)
	}
	paths, err := runtimeinput.Paths(findings[0].TargetEvidence.RuntimeInputs, tr.dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		seen[filepath.Base(path)] = true
	}
	for _, name := range []string{"input-0.txt", "input-2.txt"} {
		if !seen[name] {
			t.Fatalf("runtime paths = %v, missing %s", paths, name)
		}
	}
}

// TestAttestationShedsAcrossSourceDrift pins REQ-attest-survivor: even when
// the mutated body is unchanged, moved subject evidence requires every
// equivalence to be judged afresh.
func TestAttestationShedsAcrossSourceDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	targets := []Target{{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}}
	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	weak := first[0]
	if len(weak.Survivors) == 0 {
		t.Fatal("no survivors to attest")
	}
	s0 := weak.Survivors[0]
	if err := weak.Attest(s0.Position, s0.Operator, "equivalent"); err != nil {
		t.Fatal(err)
	}
	doc, err := Export([]Finding{weak})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Shift the declaration down one line without touching its body. The
	// maximal source closure still moves, so the prior disposition is judged
	// afresh rather than inferred to be location-only.
	libPath := filepath.Join(tmp, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	shifted := strings.Replace(string(src), "func Weak(", "// shifted by an edit above the body\nfunc Weak(", 1)
	if shifted == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(libPath, []byte(shifted), 0o644); err != nil {
		t.Fatal(err)
	}

	tr2, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// Force the re-measure so the new evidence is produced and compared.
	moved, err := tr2.Run(ctx, targets, Options{Force: true, Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	got := moved[0]
	if got.Cached {
		t.Fatal("forced run served from cache")
	}
	if len(got.Attested) != 0 {
		t.Fatalf("attestation = %+v, want shed after closure drift", got.Attested)
	}
	if len(got.Open()) != len(got.Survivors) {
		t.Fatalf("open = %d of %d survivors after disposition shedding", len(got.Open()), len(got.Survivors))
	}
}

// TestRunDuplicateTargetRefused pins the finding-key collision guard
// (REQ-result-record keys by symbol): two targets naming one symbol are
// refused up front rather than one silently shadowing the other.
func TestRunDuplicateTargetRefused(t *testing.T) {
	tr := fixtureTree(t)
	_, err := tr.Run(context.Background(), []Target{
		{Symbol: "example.com/fixture/lib.Add"},
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "duplicate target symbol") {
		t.Fatalf("duplicate targets accepted: %v", err)
	}
}

func TestRunRejectsNegativeBudget(t *testing.T) {
	tr := fixtureTree(t)
	_, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add"}}, Options{Budget: -1})
	if err == nil || !strings.Contains(err.Error(), "budget must be non-negative") {
		t.Fatalf("negative budget accepted: %v", err)
	}
}

// TestRunRejectsAmbiguousOracle pins the orchestration guard from
// REQ-target-oracle: same-named in-package and external tests cannot be mapped
// back from one displayed test event, so the run must stop before mutation.
func TestRunRejectsAmbiguousOracle(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.com/ambiguous\n\ngo 1.26\n",
		"p.go":             "package ambiguous\n\nfunc F() int { return 1 }\n",
		"internal_test.go": "package ambiguous\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n",
		"external_test.go": "package ambiguous_test\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), []Target{{
		Symbol: "example.com/ambiguous.F",
		Oracle: []string{"example.com/ambiguous.TestSame"},
	}}, Options{Budget: 1})
	if err == nil || !strings.Contains(err.Error(), "ambiguous across test package variants") {
		t.Fatalf("ambiguous oracle run = %v", err)
	}
}

// TestRunNoOracle pins the no-oracle skip: a target in a test-less package
// derives an empty oracle and is reported, never measured, never dropped.
func TestRunNoOracle(t *testing.T) {
	tr := fixtureTree(t)
	fs, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/methods.Counter.Inc"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fs[0].Skipped != "no oracle" {
		t.Fatalf("finding = %+v, want skipped with no oracle", fs[0])
	}
}

// TestParseFindingsVersionAndTolerance pins the document boundary
// (REQ-result-export, REQ-result-tolerant): an unknown version is refused;
// an unknown field within a known version is discarded.
func TestParseFindingsVersionAndTolerance(t *testing.T) {
	if _, err := ParseFindings([]byte(`{"version": 99, "findings": []}`)); err == nil {
		t.Fatal("unknown version accepted")
	}
	fs, err := ParseFindings([]byte(`{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"}],"timeout":"1m0s","dirty":true,"mutants":0,"killed":0,"futureField":{"nested":true}}]}`))
	if err != nil || len(fs) != 1 || fs[0].Symbol != "p.F" {
		t.Fatalf("tolerant parse failed: %v %+v", err, fs)
	}
	for name, doc := range map[string]string{
		"null budget":                    `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":null,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":1}]}`,
		"null dirty":                     `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","dirty":null,"mutants":1,"killed":1}]}`,
		"duplicate budget":               `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"budget":0,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":1}]}`,
		"duplicate version":              `{"version":1,"version":99,"findings":[]}`,
		"missing survivors":              `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":0}]}`,
		"empty attestation reason":       `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":""}]}]}`,
		"duplicate nested evidence":      `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{"symbol":"p.F","symbol":"p.G"},"oracleEvidence":[],"timeout":"1m0s","mutants":0,"killed":0}]}`,
		"inflated budget":                `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":2,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":1}]}`,
		"colliding attestation identity": `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"timeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"a|b.go:1:1","operator":"zero return"}],"attested":[{"position":"a","operator":"b.go:1:1|zero return","reason":"not the survivor"}]}]}`,
		"duplicate symbols":              `{"version":1,"findings":[{"symbol":"p.F","mutants":0,"killed":0},{"symbol":"p.F","mutants":0,"killed":0}]}`,
		"duplicate oracle symbols":       `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{},"oracleEvidence":[{"symbol":"p.TestF"},{"symbol":"p.TestF"}],"timeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed known field accepted")
			}
		})
	}
	nonGit := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"}],"timeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`
	nonGitFindings, err := ParseFindings([]byte(nonGit))
	if err != nil || len(nonGitFindings) != 1 {
		t.Fatalf("non-Git provenance rejected: %v %+v", err, nonGitFindings)
	}
	invalidExport := nonGitFindings[0]
	invalidExport.Dirty = false
	if _, err := Export([]Finding{invalidExport}); err == nil {
		t.Fatal("export emitted commitless clean provenance")
	}
	digestAt := strings.LastIndex(nonGit, `"runtimeDigest":"d"`)
	if digestAt < 0 {
		t.Fatal("runtime digest fixture missing")
	}
	mismatchedRuntime := nonGit[:digestAt] + `"runtimeDigest":"other"` + nonGit[digestAt+len(`"runtimeDigest":"d"`):]
	if _, err := ParseFindings([]byte(mismatchedRuntime)); err == nil {
		t.Fatal("per-subject runtime evidence mismatch accepted")
	}
	partialRuntime := nonGit[:digestAt-1] + nonGit[digestAt+len(`"runtimeDigest":"d"`):]
	if _, err := ParseFindings([]byte(partialRuntime)); err == nil {
		t.Fatal("partial runtime evidence accepted")
	}
	impossibleRuntime := strings.ReplaceAll(nonGit, `"runtimeDigest":"d"`, `"runtimeUnverifiable":true,"runtimeDigest":"d"`)
	if _, err := ParseFindings([]byte(impossibleRuntime)); err == nil {
		t.Fatal("impossible runtime disposition accepted")
	}
	wrongTarget := strings.Replace(nonGit, `"targetEvidence":{"symbol":"p.F"`, `"targetEvidence":{"symbol":"p.G"`, 1)
	if _, err := ParseFindings([]byte(wrongTarget)); err == nil {
		t.Fatal("mismatched target evidence accepted")
	}
	emptyOracle := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[],"timeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`
	if _, err := ParseFindings([]byte(emptyOracle)); err == nil {
		t.Fatal("empty oracle evidence accepted")
	}
	withoutDirty := strings.Replace(nonGit, `,"dirty":true`, "", 1)
	if _, err := ParseFindings([]byte(withoutDirty)); err == nil {
		t.Fatal("missing commit without dirty provenance accepted")
	}
	committedWithoutDirty := strings.Replace(withoutDirty, `"timeout":"1m0s"`, `"timeout":"1m0s","commit":"abc"`, 1)
	if _, err := ParseFindings([]byte(committedWithoutDirty)); err == nil {
		t.Fatal("committed finding without explicit dirty provenance accepted")
	}
	legacy := `{"version":1,"findings":[{"symbol":"p.F","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"legacy"}]}]}`
	if _, err := ParseFindings([]byte(legacy)); err == nil {
		t.Fatal("legacy finding accepted")
	}
	emptyPins := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"","operatorSet":"","budget":1,"targetEvidence":{"symbol":"","maximalClosure":"","toolchain":"","buildConfig":"","runtimeInputs":"","runtimeDigest":""},"oracleEvidence":[],"timeout":"","dirty":true,"mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"unsupported"}]}]}`
	if _, err := ParseFindings([]byte(emptyPins)); err == nil {
		t.Fatal("empty required pins accepted")
	}
}
