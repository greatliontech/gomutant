package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"golang.org/x/tools/go/packages"
)

// TestRunMutantOutcomes pins the overlay runner end to end
// (REQ-exec-oracle-run, REQ-mut-overlay): a pinned-down body kills every
// behaviorally distinct mutant, an untested branch yields survivors, every
// kill is attributed, and the tree is never touched.
func TestRunMutantOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	dir := "testdata/fixturemod"

	run := func(symbol, regex string) (killed, survived int, survivors []Mutant) {
		ms, err := tr.Mutants(symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			out, killer, _, err := RunMutant(context.Background(), dir, m, []string{"example.com/fixture/lib"}, regex, 60*time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch out {
			case MutantKilled:
				killed++
				// Every kill is attributed to the oracle test that noticed
				// (REQ-exec-attribution).
				if killer != "example.com/fixture/lib."+strings.TrimSuffix(strings.TrimPrefix(regex, "^"), "$") {
					t.Fatalf("kill attributed to %q under -run %s", killer, regex)
				}
			case MutantSurvived:
				survived++
				if killer != "" {
					t.Fatalf("survivor carries killer %q", killer)
				}
				survivors = append(survivors, m)
			}
		}
		return
	}

	killed, survived, addSurvivors := run("example.com/fixture/lib.Add", "^TestAdd$")
	wantAddSurvivors := map[string]string{
		"lib.go:24:2":  "statement: delete",
		"lib.go:24:5":  "condition: force false",
		"lib.go:24:12": "block: empty",
		"lib.go:25:3":  "statement: delete",
	}
	if survived != len(wantAddSurvivors) || killed != 7 {
		t.Fatalf("Add: killed=%d survivors=%+v, want exact go/12 counts", killed, addSurvivors)
	}
	for _, survivor := range addSurvivors {
		if wantAddSurvivors[survivor.Position] != survivor.Operator {
			t.Fatalf("unexpected Add survivor: %+v", survivor)
		}
	}
	_, survived, survivors := run("example.com/fixture/lib.Weak", "^TestWeak$")
	if survived == 0 {
		t.Fatal("Weak: the untested branch produced no survivors")
	}
	for _, s := range survivors {
		if !strings.HasPrefix(s.Position, "lib.go:") {
			t.Fatalf("survivor position not file-anchored: %s", s.Position)
		}
	}
}

func TestRunMutantObservedReturnsCompletedEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	mutants, err := tr.Mutants("example.com/fixture/lib.Add", 1)
	if err != nil || len(mutants) == 0 {
		t.Fatalf("Mutants: %v", err)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, state, incomplete, _, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[0],
		[]string{"example.com/fixture/lib"}, "^TestAdd$", 60*time.Second, nil, moduleDir, packageDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !state.OK || state.Manifest == "" || state.Digest == "" || incomplete != "" {
		t.Fatalf("observation = %+v, incomplete %q", state, incomplete)
	}
}

func TestMissingProcessLogIsIncomplete(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	state, incomplete, err := processObservation(filepath.Join(t.TempDir(), "missing.testlog"), moduleDir,
		"", GoEnv(moduleDir), true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "produced no runtime-input log") {
		t.Fatalf("missing-log observation = %+v, want explicit incompleteness", state)
	}
	if !strings.Contains(incomplete, "produced no runtime-input log") {
		t.Fatalf("missing-log incompleteness = %q, want the candidate-local reason", incomplete)
	}
}

func TestIncompleteProcessDoesNotAssertPartialLogComplete(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "partial.testlog")
	log := append([]byte("open input-0.txt\n"), bytes.Repeat([]byte{'x'}, 128<<10)...)
	if err := os.WriteFile(logPath, log, 0o644); err != nil {
		t.Fatal(err)
	}
	state, incomplete, err := processObservation(logPath, moduleDir,
		"test process timed out", GoEnv(moduleDir), true)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete != "test process timed out" {
		t.Fatalf("incomplete reason = %q, want the caller's process incompleteness", incomplete)
	}
	paths, err := runtimeinput.Paths(state.Manifest, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || len(paths) != 0 {
		t.Fatalf("partial observation = %+v, paths %v", state, paths)
	}
}

func TestObservedRunScoresAgainstStableRuntimeInputs(t *testing.T) {
	tr := fixtureTree(t)
	mutants, err := tr.Mutants("example.com/fixture/lib.Add", 1)
	if err != nil || len(mutants) != 1 {
		t.Fatalf("Mutants: %v, count %d", err, len(mutants))
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	// The input lives inside the oracle package: the pre-spawn bracket
	// covers it, so its stable value binds instead of sealing.
	input := filepath.Join(packageDir, ".moving-input-fixture")
	if err := os.WriteFile(input, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(input) })
	t.Setenv("GOMUTANT_MOVING_INPUT", input)
	outcome, killer, _, state, incomplete, _, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[0],
		[]string{"example.com/fixture/lib"}, "^TestMovingInput$", 60*time.Second, nil, moduleDir, packageDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != MutantSurvived || killer != "" {
		t.Fatalf("stable-input measurement = %v/%q, want survivor from second run", outcome, killer)
	}
	// A test mutating its own runtime input mid-run seals the
	// observation through the bracket: the settled value is exactly the
	// false-reuse class the bracket generation refuses, so the verdict
	// scores while the evidence stays unverifiable.
	if !state.OK || !state.Unverifiable || incomplete != "" || !strings.Contains(state.Reason, "observation bracket moved") {
		t.Fatalf("runtime state = %+v, incomplete %q, want bracket-sealed unverifiable evidence", state, incomplete)
	}
}

func TestNamedTestPanicIsIncompleteEvidence(t *testing.T) {
	tr := fixtureTree(t)
	mutants, err := tr.Mutants("example.com/fixture/lib.PanicValue", 0)
	if err != nil {
		t.Fatalf("Mutants: %v, count %d", err, len(mutants))
	}
	mutantIndex := slices.IndexFunc(mutants, func(m Mutant) bool { return m.Operator == "integer literal: magnitude +1" })
	if mutantIndex < 0 {
		t.Fatalf("integer literal mutant missing: %+v", mutants)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	outcome, killer, _, state, incomplete, _, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[mutantIndex],
		[]string{"example.com/fixture/lib"}, "^TestNamedPanic$", 60*time.Second, nil, moduleDir, packageDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != MutantKilled || killer != "example.com/fixture/lib.TestNamedPanic" {
		t.Fatalf("named panic = %v/%q, want attributed kill", outcome, killer)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "panicked before observation finalization") {
		t.Fatalf("named-panic observation = %+v, want explicit incompleteness", state)
	}
	if !strings.Contains(incomplete, "panicked before observation finalization") {
		t.Fatalf("named-panic incompleteness = %q, want candidate-local reason", incomplete)
	}
}

// TestRunMutantGoroutinePanicIsAKill pins the package-level attribution arm
// (REQ-exec-attribution): a mutant that detonates in a goroutine emits no
// test-level fail event — the differential baseline probe clears the
// environment and the kill is admitted with the package sentinel, never
// misread as noise.
func TestRunMutantGoroutinePanicIsAKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Guarded", 0)
	if err != nil {
		t.Fatal(err)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	pkgKills, timeoutKills := 0, 0
	for _, m := range ms {
		// Two of Guarded's mutants drop the channel send, so the receiver
		// deadlocks and the run can only end at the timeout. A short one
		// suffices — the legitimate run is sub-second — and keeps this
		// exhaustive loop from paying a long timeout for mutants that are
		// incidental to the package-kill this test asserts.
		out, killer, _, state, incomplete, _, err := RunMutantObserved(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestGuarded$", 5*time.Second, nil, moduleDir, packageDir, nil, nil)
		if err != nil {
			t.Fatalf("mutant %s %s aborted as noise: %v", m.Position, m.Operator, err)
		}
		if out == MutantKilled && strings.HasPrefix(killer, PackageKillerPrefix) {
			pkgKills++
			if !state.Unverifiable {
				t.Fatalf("package-failure observation = %+v, want unverifiable", state)
			}
			if killer != PackageKillerPrefix+"example.com/fixture/lib)" {
				t.Fatalf("sentinel = %q", killer)
			}
			if !strings.Contains(incomplete, "exited before observation finalization") {
				t.Fatalf("package-failure incompleteness = %q, want candidate-local reason", incomplete)
			}
		}
		if out == MutantKilled && killer == TimeoutKiller {
			timeoutKills++
			if !state.Unverifiable {
				t.Fatalf("timeout observation = %+v, want unverifiable", state)
			}
			if !strings.Contains(incomplete, "timed out") {
				t.Fatalf("timeout incompleteness = %q, want candidate-local reason", incomplete)
			}
		}
	}
	if pkgKills == 0 {
		t.Fatal("no mutant detonated in the goroutine; the guard mutant should")
	}
	// The dropped-send mutants deadlock: the hang is the noticed breakage,
	// killed with the timeout attribution (REQ-exec-attribution).
	if timeoutKills == 0 {
		t.Fatal("no deadlocking mutant killed by timeout; the dropped send should")
	}
}

// TestRunMutantBuildFailureIsDiscarded pins the discard arm: a mutant that
// does not compile proves nothing — never a kill, never a survivor, and
// never an abort (REQ-mut-operators' compile-discard split).
func TestRunMutantBuildFailureIsDiscarded(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Idx", 0)
	if err != nil {
		t.Fatal(err)
	}
	discarded := 0
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		out, killer, _, state, incomplete, diagnostic, err := RunMutantObserved(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestAdd$", 60*time.Second, nil, moduleDir, packageDir, nil, nil)
		if err != nil {
			t.Fatalf("mutant %s %s: %v", m.Position, m.Operator, err)
		}
		if out == MutantDiscarded && diagnostic != "" {
			discarded++
			if killer != "" {
				t.Fatalf("discarded mutant carries killer %q", killer)
			}
			// No test process started: the engine claims no runtime
			// exposure and no incomplete-process evidence — the discard
			// rides the toolchain and build-configuration pins, and the
			// diagnostic carries the compiler's own text (candidate
			// evidence term, REQ-result-stale).
			if state.OK || state.Unverifiable || state.Manifest != "" {
				t.Fatalf("build-rejected mutant claims runtime exposure: %+v", state)
			}
			if incomplete != "" {
				t.Fatalf("build-rejected mutant carries incomplete-process evidence: %q", incomplete)
			}
		}
	}
	if discarded == 0 {
		t.Fatal("no uncompilable mutant was discarded; a[1] on [1]int should be")
	}
}

// TestRunMutantNoiseIsNeverAKill pins the attribution rule that keeps kill
// counts sound (REQ-core-attributed-kills, REQ-exec-attribution): a run
// that dies without a test-attributed failure and whose differential
// baseline fails too — here, a test binary refusing an unregistered
// flag — is never a kill; it records candidate-locally with its
// diagnostic and the campaign continues instead of aborting.
func TestRunMutantNoiseIsNeverAKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Add", 1)
	if err != nil || len(ms) == 0 {
		t.Fatalf("no mutants: %v", err)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/plain")
	if err != nil {
		t.Fatal(err)
	}
	out, killer, _, state, incomplete, _, err := RunMutantObserved(context.Background(), "testdata/fixturemod", ms[0],
		[]string{"example.com/fixture/plain"}, "^TestPlain$", 60*time.Second,
		[]string{"-no.such.flag"}, moduleDir, packageDir, nil, nil)
	if err != nil {
		t.Fatalf("noise aborted the run: %v", err)
	}
	if out != MutantDiscarded || killer != "" {
		t.Fatalf("noise read as outcome %v killer %q, want a diagnosed discard", out, killer)
	}
	if !strings.Contains(incomplete, "unclassifiable mutant-run failure") || !strings.Contains(incomplete, "baseline probe failed alongside the mutant") {
		t.Fatalf("noise diagnostic = %q, want the unclassifiable reason with the probe outcome", incomplete)
	}
	if !state.Unverifiable {
		t.Fatalf("noise observation = %+v, want explicit incompleteness", state)
	}
}

// TestSplitRapidPkgs pins the rapid-failfile partition (REQ-mut-overlay's
// runtime tree purity): the flag is per-binary, so packages split by
// whether their test binary LINKS rapid — directly, via a test
// variant, or through a helper — and a mixed
// union must never put the flag in front of a rapid-free binary, which would
// die on it and read as a false kill.
func TestSplitRapidPkgs(t *testing.T) {
	tr := fixtureTree(t)
	lib, plainPkg, ext := "example.com/fixture/lib", "example.com/fixture/plain", "example.com/fixture/extprop"

	rapid, plain := tr.SplitRapidPkgs([]string{lib, plainPkg, ext})
	if len(rapid) != 2 || rapid[0] != lib || rapid[1] != ext {
		t.Fatalf("rapid group = %v (lib via in-package tests, extprop via the external variant)", rapid)
	}
	if len(plain) != 1 || plain[0] != plainPkg {
		t.Fatalf("plain group = %v", plain)
	}

	// A test driving rapid solely through a helper package links the
	// runtime all the same: the linked-closure detection classifies it
	// rapid — a direct-imports-only verdict would run it unpinned,
	// record the empty regime, and let a killed property write
	// reproducer litter (REQ-exec-property-oracles).
	indirect := "example.com/fixture/indirectprop"
	rapid, plain = tr.SplitRapidPkgs([]string{indirect, plainPkg})
	if len(rapid) != 1 || rapid[0] != indirect {
		t.Fatalf("helper-linked rapid group = %v, want the indirect package classified rapid", rapid)
	}
	if len(plain) != 1 || plain[0] != plainPkg {
		t.Fatalf("helper-linked plain group = %v", plain)
	}
	runtimes, err := tr.PropertyRuntimesContext(context.Background(), []string{indirect})
	if err != nil {
		t.Fatal(err)
	}
	if got := runtimes[indirect]; len(got) != 1 || got[0] != "rapid" {
		t.Fatalf("helper-linked runtimes = %v, want the rapid prerequisite statement", got)
	}
}

// TestFirstFailingTest pins killer derivation from the -json stream
// (REQ-exec-attribution): first test-level fail wins, subtest kills
// attribute through their top level (stripped here, where the Test field is
// unambiguous — the joined symbol's first slash lands inside the import
// path), and package-level fail events attribute nothing.
func TestFirstFailingTest(t *testing.T) {
	stream := []byte(`{"Action":"run","Package":"example.com/p","Test":"TestA"}
{"Action":"fail","Package":"example.com/p","Test":"TestA/sub/deep"}
{"Action":"fail","Package":"example.com/p","Test":"TestA"}
{"Action":"fail","Package":"example.com/p"}
`)
	if got, err := firstFailingTest(stream); err != nil || got != "example.com/p.TestA" {
		t.Fatalf("killer = %q", got)
	}
	if got, err := firstFailingTest([]byte(`{"Action":"fail","Package":"example.com/p"}` + "\n")); err != nil || got != "" {
		t.Fatalf("package-level fail attributed: %q", got)
	}
	malformed := append(stream, []byte("not-json\n")...)
	if got, err := firstFailingTest(malformed); err == nil || got != "" {
		t.Fatalf("malformed tail accepted as %q: %v", got, err)
	}
	countStream := []byte(`{"Action":"pass","Test":"TestA"}` + "\nnot-json\n")
	if count, err := countTopTests(countStream); err == nil || count != 0 {
		t.Fatalf("malformed count stream accepted: count=%d err=%v", count, err)
	}
	normalFailure := []byte(`{"Action":"output","Test":"TestA","Output":"--- FAIL: TestA (0.00s)\n"}` + "\n")
	if !testFailureCompleted(normalFailure, "TestA") {
		t.Fatal("normal harness failure read as abrupt")
	}
	if testFailureCompleted([]byte(`{"Action":"fail","Test":"TestA"}`+"\n"), "TestA") {
		t.Fatal("synthetic failure without harness marker read as complete")
	}
	parallelAbrupt := []byte(`{"Action":"run","Test":"TestA"}
{"Action":"run","Test":"TestB"}
{"Action":"output","Test":"TestA","Output":"--- FAIL: TestA (0.00s)\n"}
{"Action":"fail","Test":"TestA"}
`)
	if testFailureCompleted(parallelAbrupt, "TestA") {
		t.Fatal("unfinished parallel test read as complete")
	}
	otherMarker := []byte(`{"Action":"run","Test":"TestA"}
{"Action":"run","Test":"TestB"}
{"Action":"output","Test":"TestA","Output":"--- FAIL: TestA (0.00s)\n"}
{"Action":"fail","Test":"TestA"}
{"Action":"fail","Test":"TestB"}
`)
	if testFailureCompleted(otherMarker, "TestB") {
		t.Fatal("another test's harness marker legitimized the failing test")
	}
}

// TestProbeBaseline pins the ephemeral gate's probe (REQ-exec-ephemeral): a
// passing named test reports ran>0 and passed; a -run matching nothing
// reports ran==0, which the caller must refuse to score against.
func TestProbeBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	ran, passed, err := TestProbe(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", 60*time.Second, nil)
	if err != nil || ran != 1 || !passed {
		t.Fatalf("probe TestAdd: ran=%d passed=%v err=%v", ran, passed, err)
	}
	ran, _, err = TestProbe(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestNoSuch$", 60*time.Second, nil)
	if err != nil || ran != 0 {
		t.Fatalf("probe no-match: ran=%d err=%v", ran, err)
	}
	// A test failing on the clean tree would fail against any mutant too —
	// a fabricated kill unless the probe reports it (REQ-exec-ephemeral).
	ran, passed, err = TestProbe(context.Background(), "testdata/fixturemod", "example.com/fixture/failing", "^TestAlwaysFails$", 60*time.Second, nil)
	if err != nil || ran != 1 || passed {
		t.Fatalf("probe failing-clean: ran=%d passed=%v err=%v, want ran=1 passed=false", ran, passed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := TestProbe(ctx, "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", 60*time.Second, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe = %v", err)
	}
	tr := fixtureTree(t)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	env := GoEnv("testdata/fixturemod")
	ran, passed, _, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestPickInput$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil || ran != 1 || !passed || !state.OK || state.Unverifiable {
		t.Fatalf("observed passing baseline = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
	ran, passed, _, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestNoSuch$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil || ran != 0 || !passed {
		t.Fatalf("observed zero-match baseline = ran %d, passed %v, error %v", ran, passed, err)
	}
	failingModule, failingDir, err := tr.PackageContext("example.com/fixture/failing")
	if err != nil {
		t.Fatal(err)
	}
	var failedNames []string
	ran, passed, failedNames, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/failing", "^TestAlwaysFails$", time.Minute, nil, failingModule, failingDir, nil, nil, env)
	if len(failedNames) != 1 || failedNames[0] != "TestAlwaysFails" {
		t.Fatalf("failed-test names = %v, want the failing baseline named", failedNames)
	}
	if err != nil || ran != 1 || passed {
		t.Fatalf("observed failing baseline = ran %d, passed %v, error %v", ran, passed, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := TestProbeObservedEnv(ctx, "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", time.Minute, nil, moduleDir, packageDir, nil, nil, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled observed baseline = %v", err)
	}
}

//gofresh:pure
func TestProbeBaselineRecordsRuntimeInputDriftAsUnverifiable(t *testing.T) {
	tr := fixtureTree(t)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(packageDir, ".unstable-input-fixture")
	if err := os.WriteFile(input, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(input) })
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_UNSTABLE_INPUT="+input)
	ran, passed, _, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestUnstableInput$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	// A mid-run mutation of an in-bracket input seals through the
	// bracket (the stronger signal) or through repeated-baseline drift;
	// either way the evidence is unverifiable, never silently valid.
	drifty := strings.Contains(state.Reason, "repeated baseline executions") || strings.Contains(state.Reason, "observation bracket moved")
	if err != nil || ran != 1 || !passed || !state.OK || !state.Unverifiable || !drifty {
		t.Fatalf("unstable baseline = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
}

// Self-cleaned in-module scratch with a fresh name each run — absent at
// both bracket endpoints — binds as completed evidence carrying the
// missing-arm identity beside the stable input; the churn surfaces as
// digest movement ACROSS runs (stale, re-measure), never as a served
// stale record and never as blanket unverifiability. Mid-run drift of a
// pre-existing input keeps its own unverifiable pin in the sibling test
// above.
func TestProbeBaselineRetainsInputsWhenIdentitiesChange(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(moduleDir, "lib")
	stable := filepath.Join(packageDir, ".stable-input-fixture")
	if err := os.WriteFile(stable, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(stable) })
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_STABLE_INPUT="+stable)
	ran, passed, _, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestChangingIdentity$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil || ran != 1 || !passed || !state.OK || state.Unverifiable {
		t.Fatalf("changing identities = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
	paths, err := runtimeinput.Paths(state.Manifest, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, stable) {
		t.Fatalf("runtime paths = %v, missing stable input %s", paths, stable)
	}
	changing := 0
	for _, p := range paths {
		if strings.Contains(filepath.Base(p), ".changing-identity-") {
			changing++
		}
	}
	if changing == 0 {
		t.Fatalf("runtime paths = %v, missing the per-run changing identity", paths)
	}
	// The per-run identity makes the evidence stale across runs — the
	// honest direction: a fresh probe re-measures rather than serving.
	ran2, passed2, _, second, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestChangingIdentity$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil || ran2 != 1 || !passed2 || !second.OK {
		t.Fatalf("second changing-identity probe = ran %d, passed %v, state %+v, error %v", ran2, passed2, second, err)
	}
	if second.Digest == state.Digest {
		t.Fatal("per-run changing identity did not move the evidence digest across runs")
	}
}

// testBracket captures an observation bracket over the whole root, so
// direct testlog constructions satisfy the completed-observation
// contract exactly as the engine's pre-spawn capture does.
func testBracket(t *testing.T, root string) runtimeinput.Bracket {
	t.Helper()
	b, err := runtimeinput.CaptureBracket(root, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMergeRuntimeEvidenceMakesMovementNonReusable(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "stable")
	moving := filepath.Join(root, "moving")
	if err := os.WriteFile(stable, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moving, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	stableState, err := runtimeinput.FromTestLogEnv([]byte("open "+stable+"\n"), root, root, env, runtimeinput.WithCompletedProcess("stable"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	movingState, err := runtimeinput.FromTestLogEnv([]byte("open "+moving+"\n"), root, root, env, runtimeinput.WithCompletedProcess("moving"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moving, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := mergeRuntimeEvidence(root, env, stableState, movingState)
	if err != nil || !merged.OK || !merged.Unverifiable || !strings.Contains(merged.Reason, "could not be merged for reuse") {
		t.Fatalf("moved observation = %+v, %v", merged, err)
	}
	paths, err := runtimeinput.Paths(merged.Manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, stable) {
		t.Fatalf("runtime paths = %v, missing stable input %s", paths, stable)
	}
}

func TestAbsoluteRuntimeEvidenceDropsMovedUnsealedInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	state, err := runtimeinput.FromTestLogEnv([]byte("open "+path+"\n"), root, root, env, runtimeinput.WithCompletedProcess("absolute"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	absolute, err := absoluteRuntimeEvidence(state, root, env)
	if err != nil || !absolute.OK || !absolute.Unverifiable || !strings.Contains(absolute.Reason, "could not be finalized for reuse") {
		t.Fatalf("moved absolute observation = %+v, %v", absolute, err)
	}
	paths, err := runtimeinput.Paths(absolute.Manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("runtime paths = %v, want moved input omitted from incomplete evidence", paths)
	}
	for name, malformed := range map[string]runtimeinput.Observation{
		"empty":     {},
		"malformed": {State: runtimeinput.State{OK: true, Manifest: "malformed", Digest: "digest"}},
	} {
		t.Run(name, func(t *testing.T) {
			if state, err := absoluteRuntimeEvidence(malformed, root, env); err == nil || state.OK {
				t.Fatalf("malformed absolute observation = %+v, %v", state, err)
			}
		})
	}
}

func TestNonReusableRuntimeEvidenceDropsInputsThatMoveAgain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	state, err := runtimeinput.FromTestLogEnv([]byte("open "+path+"\n"), root, root, env, runtimeinput.WithCompletedProcess("moving"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}

	absolute, err := absoluteNonReusableRuntimeEvidence(context.Background(), state, root, env)
	if err != nil || !absolute.OK || !absolute.Unverifiable || !strings.Contains(absolute.Reason, "could not be finalized for reuse") {
		t.Fatalf("repeatedly moved observation = %+v, %v", absolute, err)
	}
	paths, err := runtimeinput.Paths(absolute.Manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("runtime paths = %v, want unstable paths discarded", paths)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := absoluteNonReusableRuntimeEvidence(ctx, absolute, root, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled conversion error = %v, want %v", err, context.Canceled)
	}
}

func TestProbeBaselineRejectsTestCountDrift(t *testing.T) {
	tr := fixtureTree(t)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/unstable")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "baseline-count")
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_UNSTABLE_COUNT="+marker)
	_, _, _, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/unstable", "^TestAdd$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err == nil || !strings.Contains(err.Error(), "baseline test count changed") {
		t.Fatalf("unstable baseline count = %v", err)
	}
}

//gofresh:pure
func TestProbeBaselineRejectsResultDrift(t *testing.T) {
	tr := fixtureTree(t)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "baseline-result")
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_UNSTABLE_RESULT="+marker)
	_, _, _, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestUnstableBaselineResult$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err == nil || !strings.Contains(err.Error(), "result changed between discovery and measurement") {
		t.Fatalf("unstable baseline result = %v", err)
	}
}

func TestLoadRefusesUnsupportedProcessExecution(t *testing.T) {
	if _, err := load(t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "supports Unix and Windows hosts") {
		t.Fatalf("unsupported process execution = %v", err)
	}
}

func TestLoadContextCancelsPackageLoading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	loaded := false
	tree, err := loadContextWith(ctx, t.TempDir(), Selection{}, true, func(cfg *packages.Config, _ ...string) ([]*packages.Package, error) {
		loaded = true
		if cfg.Context != ctx {
			t.Fatal("package loader did not receive caller context")
		}
		cancel()
		return nil, nil
	})
	if !loaded || !errors.Is(err, context.Canceled) || tree != nil {
		t.Fatalf("cancelled load = loaded %v, tree %v, error %v", loaded, tree, err)
	}
}

func TestGoTestArgs(t *testing.T) {
	got := goTestArgs(11*time.Minute, "-run", "^TestF$", "example.com/p")
	want := []string{"test", "-json", "-timeout", "11m1s", "-run", "^TestF$", "example.com/p"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("go test args = %v, want %v", got, want)
	}
	max := time.Duration(1<<63 - 1)
	got = goTestArgs(max)
	if got[3] != max.String() {
		t.Fatalf("maximum timeout wrapped to %q", got[3])
	}
}

// A complete testlog without a pre-spawn bracket degrades to an
// incomplete observation carrying the capture's stated reason: the
// values the run read cannot bind, so the evidence fails closed
// instead of erroring or silently completing.
func TestObservationWithoutBracketFailsClosedAsIncomplete(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "proc.testlog")
	if err := os.WriteFile(log, []byte("# test log\nopen fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	frame := captureOracleFrame(context.Background(), root, filepath.Join(root, "no-such-pkg"), nil)
	if frame.Reason() == "" {
		t.Fatal("capture of a nonexistent package directory produced a usable frame")
	}
	obs, incomplete, err := processObservationContext(context.Background(), log, root, "", os.Environ(), "", true, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete == "" || !strings.Contains(incomplete, "capture failed") {
		t.Fatalf("incomplete = %q, want the frame capture's stated reason", incomplete)
	}
	if !obs.OK || !obs.Unverifiable {
		t.Fatalf("observation = %+v, want fail-closed incomplete evidence", obs)
	}
}

// A test that prints a captured "[build failed]" line while dying must score
// as an attributed kill, never as a no-process build rejection: the
// classification reads the harness's build-failure event, which test output
// cannot forge (candidate evidence term's harness-witness sentence).
func TestRunMutantForgedBuildFailureOutputStaysAKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/forgery.Guarded", 0)
	if err != nil {
		t.Fatal(err)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/forgery")
	if err != nil {
		t.Fatal(err)
	}
	killed := 0
	for _, m := range ms {
		out, killer, _, _, _, diagnostic, err := RunMutantObserved(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/forgery"}, "^TestGuarded$", 60*time.Second, nil, moduleDir, packageDir, nil, nil)
		if err != nil {
			t.Fatalf("mutant %s %s: %v", m.Position, m.Operator, err)
		}
		if out == MutantKilled {
			killed++
			if killer != "example.com/fixture/forgery.TestGuarded" {
				t.Fatalf("kill attributed to %q", killer)
			}
			if diagnostic != "" {
				t.Fatalf("forged output classified as a build rejection: %q", diagnostic)
			}
		}
	}
	if killed == 0 {
		t.Fatal("no mutant was killed; the boolean flip should die printing the forged marker")
	}
}

// A baseline probe that cannot complete within the oracle timeout
// proves nothing about a package-scope failure: the candidate discards
// with its diagnostic riding the candidate-local channel and the
// campaign continues — the field shape where one slow probe aborted a
// 5.5-hour run with "context deadline exceeded"
// (REQ-exec-attribution's unclassifiable arm).
func TestBaselineProbeTimeoutDiscardsAsUnclassifiable(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.StallGuard", 0)
	if err != nil {
		t.Fatal(err)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_BASELINE_STALL=1")
	unclassifiable := 0
	for _, m := range ms {
		out, killer, _, _, incomplete, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestBaselineStall$", 4*time.Second, nil, moduleDir, packageDir, nil, nil, env)
		if err != nil {
			t.Fatalf("mutant %s %s aborted the campaign: %v", m.Position, m.Operator, err)
		}
		if out == MutantDiscarded && strings.Contains(incomplete, "baseline probe exceeded the oracle timeout") {
			if killer != "" {
				t.Fatalf("unclassifiable discard carries killer %q", killer)
			}
			unclassifiable++
		}
	}
	if unclassifiable == 0 {
		t.Fatal("no mutant reached the stalling baseline probe; the zeroed StallGuard should")
	}
}

// A deadline expiry alone attributes nothing: only the oracle bound's
// own firing is a timeout. A caller's command timeout expiring mid-run
// is a cancellation — scored as a timeout kill (or as the baseline's
// oracle-bound refusal) it would fabricate evidence naming a bound
// that never fired (REQ-exec-attribution's cause discrimination; a Go
// child context inherits the parent's DeadlineExceeded verbatim, so
// only the timeout cause can tell the two apart).
func TestParentDeadlineIsCancellationNotTimeoutKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	dir := "testdata/fixturemod"
	env := append(GoEnv(dir), "GOMUTANT_BASELINE_STALL=1")

	// The oracle bound firing on the baseline probe is the typed
	// refusal naming the bound.
	_, _, err := TestProbeEnv(context.Background(), dir, "example.com/fixture/lib", "^TestBaselineStall$", 3*time.Second, nil, env)
	var bt *BaselineTimeoutError
	if !errors.As(err, &bt) {
		t.Fatalf("oracle-bound expiry = %v, want the baseline-timeout refusal", err)
	}

	// The parent deadline firing under a generous oracle bound is a
	// cancellation, never the oracle bound's refusal.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err = TestProbeEnv(ctx, dir, "example.com/fixture/lib", "^TestBaselineStall$", 10*time.Minute, nil, env)
	if err == nil {
		t.Fatal("parent-deadline baseline probe returned no error")
	}
	if errors.As(err, &bt) {
		t.Fatalf("parent expiry read as the oracle bound firing: %v", err)
	}

	// The parent deadline firing during a mutant run is never a kill:
	// the mutant leaves StallGuard intact, so the stalling arm runs and
	// the 3s parent expires far inside the 10m oracle bound.
	ms, err := tr.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no Add mutants generated")
	}
	mctx, mcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer mcancel()
	out, killer, _, err := RunMutantEnv(mctx, dir, ms[0], []string{"example.com/fixture/lib"}, "^TestBaselineStall$", 10*time.Minute, nil, env)
	if out == MutantKilled || killer == TimeoutKiller {
		t.Fatalf("parent expiry scored as a kill: outcome=%v killer=%q", out, killer)
	}
	if err == nil {
		t.Fatal("parent-deadline mutant run reported no error — either the cancellation read as a sound measurement, or the first Add mutant failed to compile (an operator-set reordering; pick a compiling mutant)")
	}
}

// The timeout attribution needs a process the bound KILLED (ExitCode
// -1: never exited on its own), not merely a failed one under the
// bound's cause: the timer can fire during post-exit teardown (scratch
// sweep, observation finalization), and relabeling a self-exited
// process — a clean pass scored as a timeout kill, or a
// test-attributed failure scored as "(timeout)" and dodging the
// oracle-set gate — fabricates evidence. The cancellation twin scores
// a completed measurement from its output even when the context died
// in teardown, and discards only genuinely failed runs
// (REQ-exec-attribution's cause discrimination).
func TestOracleBudgetFiredDiscriminates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs child processes for real exit states")
	}
	if runtime.GOOS == "windows" {
		t.Skip("exit-state fixtures use sh; the shared predicate is platform-independent and the windows killed fact is the wrapper's own cancelled flag")
	}
	bg := context.Background()
	exited := commandContext(bg, "sh", "-c", "true")
	_ = exited.Run()
	failed := commandContext(bg, "sh", "-c", "exit 1")
	failedErr := failed.Run()
	killed := commandContext(bg, "sh", "-c", "kill -KILL $$")
	killedErr := killed.Run()
	if exited.ProcessState == nil || failed.ProcessState == nil || killed.ProcessState == nil || failedErr == nil || killedErr == nil {
		t.Fatal("fixture processes did not produce the three exit states")
	}
	// The platform-owned killed fact: only the signal-killed process
	// reads as "did not exit on its own".
	if !oracleProcessKilled(killed) || oracleProcessKilled(exited) || oracleProcessKilled(failed) {
		t.Fatalf("oracleProcessKilled = killed:%v exited:%v failed:%v, want true/false/false", oracleProcessKilled(killed), oracleProcessKilled(exited), oracleProcessKilled(failed))
	}
	budget, cancel := context.WithTimeoutCause(context.Background(), -time.Second, errOracleBudgetExceeded)
	defer cancel()
	<-budget.Done()
	if !oracleBudgetFired(killedErr, killed.ProcessState, oracleProcessKilled(killed), budget) {
		t.Fatal("signal-killed process under the fired bound did not attribute as a timeout")
	}
	if oracleBudgetFired(nil, exited.ProcessState, oracleProcessKilled(exited), budget) {
		t.Fatal("cleanly exited process attributed as a timeout — teardown expiry fabricates a kill")
	}
	if oracleBudgetFired(failedErr, failed.ProcessState, oracleProcessKilled(failed), budget) {
		t.Fatal("self-exited failing process attributed as a timeout — a test-attributed kill relabeled \"(timeout)\" dodges the oracle-set gate")
	}
	// Never-started splits on EVIDENCE: Start returning the context's
	// own expiry is the bound preventing the start (the sub-startup
	// explicit timeout); any other start failure is environmental
	// noise even when the bound's cause is set by check time.
	if !oracleBudgetFired(fmt.Errorf("starting oracle: %w", context.DeadlineExceeded), nil, false, budget) {
		t.Fatal("bound-prevented start did not attribute — a sub-startup explicit timeout must refuse as the bound firing")
	}
	if oracleBudgetFired(errors.New("fork/exec /usr/bin/go: resource temporarily unavailable"), nil, false, budget) {
		t.Fatal("fork-level start failure attributed as a timeout — environmental noise scored as a kill")
	}
	parent, pcancel := context.WithTimeout(context.Background(), -time.Second)
	defer pcancel()
	child, ccancel := context.WithTimeoutCause(parent, time.Hour, errOracleBudgetExceeded)
	defer ccancel()
	<-child.Done()
	if oracleBudgetFired(killedErr, killed.ProcessState, oracleProcessKilled(killed), child) {
		t.Fatal("parent expiry attributed as the oracle bound firing")
	}

	if !oracleRunCancelled(killedErr, child) {
		t.Fatal("failed run under a dead context did not discard as cancelled")
	}
	if oracleRunCancelled(nil, child) {
		t.Fatal("completed measurement discarded as cancelled — a teardown-window expiry threw away sound evidence")
	}
	live := context.Background()
	if oracleRunCancelled(killedErr, live) {
		t.Fatal("failed run under a live context discarded as cancelled instead of being scored")
	}
}

// Tool-owned bookkeeping directories are excluded from the oracle
// bracket: an orchestrating corpus check or gomutant's own findings
// commit writing mid-span must not move a module-root package's
// bracket - without the exclusion every such write seals the
// observation (REQ-exec-observation).
func TestOracleFrameExcludesToolBookkeeping(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/toolx\n\ngo 1.26.4\n",
		"p.go":   "package toolx\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	frame := captureOracleFrame(context.Background(), dir, dir, nil)
	if frame.Reason() != "" {
		t.Fatalf("frame refused: %q", frame.Reason())
	}
	for _, write := range []string{".gomutant/findings.json", ".stipulator/witness.state"} {
		path := filepath.Join(dir, filepath.FromSlash(write))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("mid-span tool write"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(t.TempDir(), "proc.testlog")
	if err := os.WriteFile(logPath, []byte("# test log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	obs, reason, err := processObservationContext(context.Background(), logPath, dir, "", GoEnv(dir), "", true, frame, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" || obs.Unverifiable {
		t.Fatalf("mid-span tool bookkeeping moved the bracket: reason=%q obs=%q", reason, obs.Reason)
	}
}
