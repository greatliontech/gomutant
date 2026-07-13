package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"golang.org/x/tools/go/packages"
)

// TestRunMutantOutcomes pins the overlay runner end to end
// (REQ-exec-oracle-run, REQ-mut-overlay): a pinned-down body kills every
// mutant, an untested branch yields survivors, every kill is attributed, and
// the tree is never touched.
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
			out, killer, err := RunMutant(context.Background(), dir, m, []string{"example.com/fixture/lib"}, regex, 60*time.Second, nil)
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

	killed, survived, _ := run("example.com/fixture/lib.Add", "^TestAdd$")
	if survived != 0 || killed == 0 {
		t.Fatalf("Add: killed=%d survived=%d — the pinned body should kill all", killed, survived)
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
	_, _, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[0],
		[]string{"example.com/fixture/lib"}, "^TestAdd$", 60*time.Second, nil, moduleDir, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if !state.OK || state.Manifest == "" || state.Digest == "" {
		t.Fatalf("observation = %+v", state)
	}
}

func TestMissingProcessLogIsIncomplete(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	state, err := processObservation(filepath.Join(t.TempDir(), "missing.testlog"), moduleDir,
		filepath.Join(moduleDir, "lib"), "", GoEnv(moduleDir), true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "produced no runtime-input log") {
		t.Fatalf("missing-log observation = %+v, want explicit incompleteness", state)
	}
}

func TestIncompleteProcessRetainsPartialLog(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(moduleDir, "lib", "input-0.txt")
	logPath := filepath.Join(t.TempDir(), "partial.testlog")
	log := append([]byte("open input-0.txt\n"), bytes.Repeat([]byte{'x'}, 128<<10)...)
	if err := os.WriteFile(logPath, log, 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := processObservation(logPath, moduleDir, filepath.Join(moduleDir, "lib"),
		"test process timed out", GoEnv(moduleDir), true)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := runtimeinput.Paths(state.Manifest, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable || len(paths) != 1 || paths[0] != input {
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
	input := filepath.Join(t.TempDir(), "moving-input")
	if err := os.WriteFile(input, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMUTANT_MOVING_INPUT", input)
	outcome, killer, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[0],
		[]string{"example.com/fixture/lib"}, "^TestMovingInput$", 60*time.Second, nil, moduleDir, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != MutantSurvived || killer != "" {
		t.Fatalf("stable-input measurement = %v/%q, want survivor from second run", outcome, killer)
	}
	if !state.OK || state.Unverifiable {
		t.Fatalf("runtime state = %+v", state)
	}
}

func TestNamedTestPanicIsIncompleteEvidence(t *testing.T) {
	tr := fixtureTree(t)
	mutants, err := tr.Mutants("example.com/fixture/lib.PanicValue", 1)
	if err != nil || len(mutants) != 1 {
		t.Fatalf("Mutants: %v, count %d", err, len(mutants))
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	outcome, killer, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", mutants[0],
		[]string{"example.com/fixture/lib"}, "^TestNamedPanic$", 60*time.Second, nil, moduleDir, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != MutantKilled || killer != "example.com/fixture/lib.TestNamedPanic" {
		t.Fatalf("named panic = %v/%q, want attributed kill", outcome, killer)
	}
	if !state.Unverifiable || !strings.Contains(state.Reason, "panicked before observation finalization") {
		t.Fatalf("named-panic observation = %+v, want explicit incompleteness", state)
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
		out, killer, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestGuarded$", 5*time.Second, nil, moduleDir, packageDir)
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
		}
		if out == MutantKilled && killer == TimeoutKiller {
			timeoutKills++
			if !state.Unverifiable {
				t.Fatalf("timeout observation = %+v, want unverifiable", state)
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
		out, killer, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestAdd$", 60*time.Second, nil, moduleDir, packageDir)
		if err != nil {
			t.Fatalf("mutant %s %s: %v", m.Position, m.Operator, err)
		}
		if out == MutantDiscarded {
			discarded++
			if killer != "" {
				t.Fatalf("discarded mutant carries killer %q", killer)
			}
			if !state.Unverifiable || !strings.Contains(state.Reason, "failed to build") {
				t.Fatalf("discarded mutant observation = %+v, want explicit build incompleteness", state)
			}
		}
	}
	if discarded == 0 {
		t.Fatal("no uncompilable mutant was discarded; a[1] on [1]int should be")
	}
}

// TestRunMutantNoiseIsNeverAKill pins the attribution rule that keeps kill
// counts sound (REQ-core-attributed-kills): a run that dies without a
// test-attributed failure — here, a test binary refusing an unregistered
// flag — is an error, never a kill.
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
	out, killer, state, err := RunMutantObserved(context.Background(), "testdata/fixturemod", ms[0],
		[]string{"example.com/fixture/plain"}, "^TestPlain$", 60*time.Second,
		[]string{"-no.such.flag"}, moduleDir, packageDir)
	if err == nil || !strings.Contains(err.Error(), "no test-attributed kill") {
		t.Fatalf("noise read as outcome %v killer %q err %v", out, killer, err)
	}
	if !state.Unverifiable {
		t.Fatalf("noise observation = %+v, want explicit incompleteness", state)
	}
}

// TestSplitRapidPkgs pins the rapid-failfile partition (REQ-mut-overlay's
// runtime tree purity): the flag is per-binary, so packages split by whether
// their test files (in-package or external variant) import rapid — a mixed
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
	ran, passed, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestPickInput$", time.Minute, nil, moduleDir, packageDir, env)
	if err != nil || ran != 1 || !passed || !state.OK || state.Unverifiable {
		t.Fatalf("observed passing baseline = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
	ran, passed, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestNoSuch$", time.Minute, nil, moduleDir, packageDir, env)
	if err != nil || ran != 0 || !passed {
		t.Fatalf("observed zero-match baseline = ran %d, passed %v, error %v", ran, passed, err)
	}
	failingModule, failingDir, err := tr.PackageContext("example.com/fixture/failing")
	if err != nil {
		t.Fatal(err)
	}
	ran, passed, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/failing", "^TestAlwaysFails$", time.Minute, nil, failingModule, failingDir, env)
	if err != nil || ran != 1 || passed {
		t.Fatalf("observed failing baseline = ran %d, passed %v, error %v", ran, passed, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := TestProbeObservedEnv(ctx, "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", time.Minute, nil, moduleDir, packageDir, env); !errors.Is(err, context.Canceled) {
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
	input := filepath.Join(t.TempDir(), "unstable-input")
	if err := os.WriteFile(input, []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_UNSTABLE_INPUT="+input)
	ran, passed, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestUnstableInput$", time.Minute, nil, moduleDir, packageDir, env)
	if err != nil || ran != 1 || !passed || !state.OK || !state.Unverifiable || !strings.Contains(state.Reason, "repeated baseline executions") {
		t.Fatalf("unstable baseline = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
}

func TestProbeBaselineRetainsInputsWhenIdentitiesChange(t *testing.T) {
	moduleDir, err := filepath.Abs("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(moduleDir, "lib")
	stable := filepath.Join(t.TempDir(), "stable-input")
	if err := os.WriteFile(stable, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_STABLE_INPUT="+stable)
	ran, passed, state, err := TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestChangingIdentity$", time.Minute, nil, moduleDir, packageDir, env)
	if err != nil || ran != 1 || !passed || !state.OK || !state.Unverifiable || !strings.Contains(state.Reason, "repeated baseline executions") {
		t.Fatalf("changing identities = ran %d, passed %v, state %+v, error %v", ran, passed, state, err)
	}
	paths, err := runtimeinput.Paths(state.Manifest, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, stable) {
		t.Fatalf("runtime paths = %v, missing stable input %s", paths, stable)
	}
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
	stableState, err := runtimeinput.FromTestLogEnv([]byte("open "+stable+"\n"), root, root, env)
	if err != nil {
		t.Fatal(err)
	}
	movingState, err := runtimeinput.FromTestLogEnv([]byte("open "+moving+"\n"), root, root, env)
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

func TestAbsoluteRuntimeEvidenceMakesMovementNonReusable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	state, err := runtimeinput.FromTestLogEnv([]byte("open "+path+"\n"), root, root, env)
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
	if !slices.Contains(paths, path) {
		t.Fatalf("runtime paths = %v, missing %s", paths, path)
	}
	for name, malformed := range map[string]runtimeinput.State{
		"empty":     {},
		"malformed": {OK: true, Manifest: "malformed", Digest: "digest"},
	} {
		t.Run(name, func(t *testing.T) {
			if state, err := absoluteRuntimeEvidence(malformed, root, env); err == nil || state.OK {
				t.Fatalf("malformed absolute observation = %+v, %v", state, err)
			}
		})
	}
}

func TestProbeBaselineRejectsTestCountDrift(t *testing.T) {
	tr := fixtureTree(t)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "baseline-count")
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_UNSTABLE_COUNT="+marker)
	_, _, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestAdd$", time.Minute, nil, moduleDir, packageDir, env)
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
	_, _, _, err = TestProbeObservedEnv(context.Background(), "testdata/fixturemod", "example.com/fixture/lib", "^TestUnstableBaselineResult$", time.Minute, nil, moduleDir, packageDir, env)
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
	tree, err := loadContextWith(ctx, t.TempDir(), true, func(cfg *packages.Config, _ ...string) ([]*packages.Package, error) {
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
