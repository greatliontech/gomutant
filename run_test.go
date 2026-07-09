package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if add.BodyHash == "" || add.Toolchain == "" || add.OperatorSet == "" || len(add.Oracle) != 1 {
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
	// attestation: every body version's equivalences are re-judged
	// (REQ-result-stale, REQ-attest-survivor).
	tampered := append([]Finding(nil), prior...)
	for i := range tampered {
		tampered[i].BodyHash = "not-the-current-body"
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

// TestAttestationRebasesAcrossDrift pins the rebase wiring
// (REQ-attest-survivor): an edit above the declaration shifts every absolute
// position, and a carried disposition rebases against the recorded anchor
// instead of being shed — drift outside the body never sheds a disposition.
func TestAttestationRebasesAcrossDrift(t *testing.T) {
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

	// Shift the declaration down one line without touching any body: the
	// body hash holds (pins match), every absolute position moves by one.
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

	// A disposition naming a mutant that is no longer a survivor must be
	// dropped by the open-membership filter, not carried on faith.
	prior[0].Attested = append(prior[0].Attested, Attestation{Position: "lib.go:1:1", Operator: "no-op", Reason: "stale"})

	tr2, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// Force the re-measure: with pins unmoved a plain run would serve the
	// cache and never reach the rebase.
	moved, err := tr2.Run(ctx, targets, Options{Force: true, Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	got := moved[0]
	if got.Cached {
		t.Fatal("forced run served from cache")
	}
	want, ok := shiftPos(s0.Position, 1)
	if !ok {
		t.Fatal(err)
	}
	if len(got.Attested) != 1 || got.Attested[0].Position != want {
		t.Fatalf("attestation = %+v, want carried at %s", got.Attested, want)
	}
	if len(got.Open()) != len(got.Survivors)-1 {
		t.Fatalf("open = %d of %d survivors; the rebased disposition must close one", len(got.Open()), len(got.Survivors))
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
	fs, err := ParseFindings([]byte(`{"version": 1, "findings": [
		{"symbol": "p.F", "bodyHash": "h", "operatorSet": "go/2", "toolchain": "tc",
		 "mutants": 3, "killed": 3, "futureField": {"nested": true}}
	]}`))
	if err != nil || len(fs) != 1 || fs[0].Symbol != "p.F" {
		t.Fatalf("tolerant parse failed: %v %+v", err, fs)
	}
}

// TestShiftPos pins position rebasing (REQ-attest-survivor): line moves by
// the anchor delta, malformed positions report false.
func TestShiftPos(t *testing.T) {
	if got, ok := shiftPos("lib.go:10:5", 3); !ok || got != "lib.go:13:5" {
		t.Fatalf("shiftPos = %q %v", got, ok)
	}
	if _, ok := shiftPos("garbage", 1); ok {
		t.Fatal("malformed position rebased")
	}
}
