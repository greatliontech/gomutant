package engine

import (
	"context"
	"strings"
	"testing"
	"time"
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
	pkgKills, timeoutKills := 0, 0
	for _, m := range ms {
		// Two of Guarded's mutants drop the channel send, so the receiver
		// deadlocks and the run can only end at the timeout. A short one
		// suffices — the legitimate run is sub-second — and keeps this
		// exhaustive loop from paying a long timeout for mutants that are
		// incidental to the package-kill this test asserts.
		out, killer, err := RunMutant(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestGuarded$", 5*time.Second, nil)
		if err != nil {
			t.Fatalf("mutant %s %s aborted as noise: %v", m.Position, m.Operator, err)
		}
		if out == MutantKilled && strings.HasPrefix(killer, PackageKillerPrefix) {
			pkgKills++
			if killer != PackageKillerPrefix+"example.com/fixture/lib)" {
				t.Fatalf("sentinel = %q", killer)
			}
		}
		if out == MutantKilled && killer == TimeoutKiller {
			timeoutKills++
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
	for _, m := range ms {
		out, killer, err := RunMutant(context.Background(), "testdata/fixturemod", m,
			[]string{"example.com/fixture/lib"}, "^TestAdd$", 60*time.Second, nil)
		if err != nil {
			t.Fatalf("mutant %s %s: %v", m.Position, m.Operator, err)
		}
		if out == MutantDiscarded {
			discarded++
			if killer != "" {
				t.Fatalf("discarded mutant carries killer %q", killer)
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
	out, killer, err := RunMutant(context.Background(), "testdata/fixturemod", ms[0],
		[]string{"example.com/fixture/plain"}, "^TestPlain$", 60*time.Second,
		[]string{"-no.such.flag"})
	if err == nil || !strings.Contains(err.Error(), "no test-attributed kill") {
		t.Fatalf("noise read as outcome %v killer %q err %v", out, killer, err)
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
	if got := firstFailingTest(stream); got != "example.com/p.TestA" {
		t.Fatalf("killer = %q", got)
	}
	if got := firstFailingTest([]byte(`{"Action":"fail","Package":"example.com/p"}` + "\n")); got != "" {
		t.Fatalf("package-level fail attributed: %q", got)
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
}
