package engine

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// The per-tree width is host width over the job count, floored at one
// (REQ-exec-oracle-parallelism).
func TestOracleParallelismWidth(t *testing.T) {
	n := runtime.NumCPU()
	for _, test := range []struct {
		jobs, want int
	}{
		{jobs: 1, want: n},
		{jobs: 2, want: max(1, n/2)},
		{jobs: n, want: 1},
		{jobs: 10 * n, want: 1},
		{jobs: 0, want: n},
		{jobs: -3, want: n},
	} {
		if got := oracleParallelismWidth(test.jobs); got != test.want {
			t.Errorf("oracleParallelismWidth(%d) = %d, want %d", test.jobs, got, test.want)
		}
	}
}

// The cap rides the oracle environment as GOMAXPROCS and only ever
// narrows: an uninstalled cap and an already-narrower environment both
// leave the env untouched, while a wider or malformed ambient value is
// overridden by the appended entry, which wins os/exec's duplicate-key
// resolution (REQ-exec-oracle-parallelism).
func TestOracleCPUEnv(t *testing.T) {
	oracleParallelWidth.Store(0)
	if env := oracleCPUEnv([]string{"A=1"}); len(env) != 1 {
		t.Fatalf("uninstalled cap touched the env: %v", env)
	}
	oracleParallelWidth.Store(4)
	t.Cleanup(func() { oracleParallelWidth.Store(0) })
	for _, test := range []struct {
		name string
		env  []string
		want string // appended entry, "" = untouched
	}{
		{name: "absent", env: []string{"A=1"}, want: "GOMAXPROCS=4"},
		{name: "narrower ambient kept", env: []string{"GOMAXPROCS=2"}, want: ""},
		{name: "equal ambient kept", env: []string{"GOMAXPROCS=4"}, want: ""},
		{name: "wider ambient narrowed", env: []string{"GOMAXPROCS=64"}, want: "GOMAXPROCS=4"},
		{name: "malformed ambient narrowed", env: []string{"GOMAXPROCS=lots"}, want: "GOMAXPROCS=4"},
		{name: "nonpositive ambient narrowed", env: []string{"GOMAXPROCS=0"}, want: "GOMAXPROCS=4"},
		{name: "last duplicate decides", env: []string{"GOMAXPROCS=64", "GOMAXPROCS=2"}, want: ""},
	} {
		got := oracleCPUEnv(test.env)
		if test.want == "" {
			if len(got) != len(test.env) {
				t.Errorf("%s: env touched: %v", test.name, got)
			}
			continue
		}
		if len(got) != len(test.env)+1 || got[len(got)-1] != test.want {
			t.Errorf("%s: env = %v, want %s appended", test.name, got, test.want)
		}
	}
	// A lowercase key is a different variable on Unix and must not
	// suppress the cap; on Windows the same entry IS the variable and
	// an already-narrower value is kept.
	lower := oracleCPUEnv([]string{"gomaxprocs=2"})
	if runtime.GOOS == "windows" {
		if len(lower) != 1 {
			t.Errorf("windows lowercase ambient not kept: %v", lower)
		}
	} else if len(lower) != 2 || lower[1] != "GOMAXPROCS=4" {
		t.Errorf("unix lowercase key suppressed the cap: %v", lower)
	}
}

// On Windows the ambient lookup folds key case - a lowercase
// gomaxprocs entry is the same variable there, and missing it would
// append a wider entry that case-insensitive dedup lets win; Unix keys
// never fold (REQ-exec-oracle-parallelism).
func TestEnvGOMAXPROCSKeyCase(t *testing.T) {
	env := []string{"gomaxprocs=2"}
	if v, ok := envGOMAXPROCSFold(env, true); !ok || v != 2 {
		t.Fatalf("folded lookup = %d/%v, want 2/true", v, ok)
	}
	if _, ok := envGOMAXPROCSFold(env, false); ok {
		t.Fatal("case-sensitive lookup matched a lowercase key")
	}
}

// A snapshot restores the exact prior width, so a scoped override (a
// probe between campaigns) leaves no residue.
func TestOracleParallelismSnapshotRoundTrip(t *testing.T) {
	oracleParallelWidth.Store(0)
	before := SnapshotOracleParallelism()
	SetOracleParallelism(runtime.NumCPU())
	snap := SnapshotOracleParallelism()
	RestoreOracleParallelism(before)
	if OracleParallelismWidth() != 0 {
		t.Fatalf("restore left width %d, want uninstalled", OracleParallelismWidth())
	}
	RestoreOracleParallelism(snap)
	if OracleParallelismWidth() != 1 {
		t.Fatalf("restore left width %d, want 1", OracleParallelismWidth())
	}
	oracleParallelWidth.Store(0)
}

// Merging re-evaluates children against the oracle evidence env - the
// injected width included - so a width-reading oracle's observation
// merges cleanly instead of reading as moved and silently degrading
// the union to unverifiable on exactly the differential-attribution
// path (REQ-exec-oracle-parallelism).
func TestMergePreservesWidthReadingEvidence(t *testing.T) {
	SetOracleParallelism(runtime.NumCPU()) // width 1: injection guaranteed
	t.Cleanup(func() { oracleParallelWidth.Store(0) })
	root := t.TempDir()
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMAXPROCS=") {
			env = append(env, kv)
		}
	}
	obs, err := runtimeinput.FromTestLogEnv([]byte("getenv GOMAXPROCS\n"), root, root, OracleEvidenceEnv(env), runtimeinput.WithCompletedProcess("width"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeProcessObservations(root, env, true, obs)
	if err != nil {
		t.Fatal(err)
	}
	if !merged.OK || merged.Unverifiable {
		t.Fatalf("width-reading child degraded in the merge: %+v", merged)
	}
}

// The ingest mirror carries the injected GOMAXPROCS: an oracle that
// observably reads the width must have the value it actually saw
// recorded as runtime-input evidence, so width-sensitive verdicts
// stale exactly when the width moves (REQ-exec-oracle-parallelism).
func TestOracleIngestEnvCarriesInnerParallelismCap(t *testing.T) {
	oracleParallelWidth.Store(4)
	t.Cleanup(func() { oracleParallelWidth.Store(0) })
	frame := runtimeinput.ProducerFrame{PkgDir: "/pkg"}
	env := oracleIngestEnv([]string{"A=1", "PWD=/elsewhere"}, frame)
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "PWD=/pkg") || !strings.Contains(joined, "GOMAXPROCS=4") {
		t.Fatalf("mirror = %v, want PWD pinned and the effective GOMAXPROCS", env)
	}
	if refused := oracleIngestEnv([]string{"A=1"}, runtimeinput.ProducerFrame{}); !strings.Contains(strings.Join(refused, " "), "GOMAXPROCS=4") {
		t.Fatalf("refused-frame mirror = %v, want the effective GOMAXPROCS", refused)
	}
	oracleParallelWidth.Store(0)
	if plain := oracleIngestEnv([]string{"A=1"}, frame); strings.Contains(strings.Join(plain, " "), "GOMAXPROCS") {
		t.Fatalf("uninstalled cap reached the mirror: %v", plain)
	}
}

// The cap genuinely reaches the oracle's own environment: this mutant
// misbehaves ONLY when GOMAXPROCS is not the installed width, so it
// survives when the spawn wiring delivered the cap and is killed when
// it did not (REQ-exec-oracle-parallelism).
func TestOracleEnvCarriesInnerParallelismCap(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Weak", 0)
	if err != nil || len(ms) == 0 {
		t.Fatalf("no Weak mutants: %v", err)
	}
	seed := ms[0]
	original, err := os.ReadFile("testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	const weakBody = "func Weak(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}"
	if !strings.Contains(string(original), weakBody) {
		t.Fatal("fixture Weak body moved; update the sensing replacement")
	}
	sensingBody := "func Weak(x int) int {\n" +
		"\tif os.Getenv(\"GOMAXPROCS\") != \"1\" {\n" +
		"\t\treturn x + 1000\n" +
		"\t}\n" +
		"\tif x > 100 {\n" +
		"\t\treturn x - 1\n" +
		"\t}\n" +
		"\treturn x\n" +
		"}"
	sensing := strings.Replace(string(original), weakBody, sensingBody, 1)
	sensing = strings.Replace(sensing, "package lib\n", "package lib\n\nimport \"os\"\n", 1)

	// Width 1: jobs at the full host width leaves one thread per tree.
	SetOracleParallelism(runtime.NumCPU())
	t.Cleanup(func() { oracleParallelWidth.Store(0) })
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	m := Mutant{
		Symbol: seed.Symbol, Operator: "hand: parallelism-sensing", Position: seed.Position,
		Replacements: []Replacement{{File: seed.Replacements[0].File, Source: []byte(sensing)}},
	}
	env := make([]string, 0, len(GoEnv("testdata/fixturemod")))
	for _, kv := range GoEnv("testdata/fixturemod") {
		// An ambient GOMAXPROCS would make the sensing arm vacuous: the
		// mutant must see only what the cap wiring injects.
		if !strings.HasPrefix(kv, "GOMAXPROCS=") {
			env = append(env, kv)
		}
	}
	out, killer, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, "^TestWeak$", 120*time.Second, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil {
		t.Fatalf("sensing mutant aborted the campaign: %v", err)
	}
	if out != MutantSurvived {
		t.Fatalf("sensing mutant outcome = %v (killer %q): GOMAXPROCS did not reach the oracle environment", out, killer)
	}
}

// Differential attribution is sound only when the mutant run and its
// baseline probe execute under identical resource bounds
// (REQ-exec-attribution-symmetry). This mutant breaks the binary at
// package scope, forcing the baseline probe; the selection includes the
// fixture's cap-sensing test, so the baseline-probe site is
// discriminated directly: a capped baseline passes and the kill lands
// with the package sentinel, while an uncapped baseline fails
// alongside the mutant and reads as a noise discard - the negative arm
// below pins that direction, which also proves the armed sentinel
// genuinely runs rather than skips on the baseline. (The mutant-site
// env wiring is pinned separately by the sensing-survivor test:
// TestWeak runs before the sentinel in file order, so the exiting
// mutant kills the binary before the sentinel could mislabel the
// killer.)
func TestBaselineProbeRunsUnderOracleBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant plus its baseline probe")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Weak", 0)
	if err != nil || len(ms) == 0 {
		t.Fatalf("no Weak mutants: %v", err)
	}
	seed := ms[0]
	original, err := os.ReadFile("testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	const weakBody = "func Weak(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}"
	if !strings.Contains(string(original), weakBody) {
		t.Fatal("fixture Weak body moved; update the exiting replacement")
	}
	exiting := strings.Replace(string(original), weakBody, "func Weak(x int) int {\n\tos.Exit(3)\n\treturn x\n}", 1)
	exiting = strings.Replace(exiting, "package lib\n", "package lib\n\nimport \"os\"\n", 1)

	SetOracleParallelism(runtime.NumCPU())
	t.Cleanup(func() { oracleParallelWidth.Store(0) })
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	m := Mutant{
		Symbol: seed.Symbol, Operator: "hand: package-scope exit", Position: seed.Position,
		Replacements: []Replacement{{File: seed.Replacements[0].File, Source: []byte(exiting)}},
	}
	env := make([]string, 0, len(GoEnv("testdata/fixturemod"))+1)
	for _, kv := range GoEnv("testdata/fixturemod") {
		// An ambient GOMAXPROCS would make the sensing test vacuous.
		if !strings.HasPrefix(kv, "GOMAXPROCS=") {
			env = append(env, kv)
		}
	}
	// Arm the fixture's cap assertion; it skips in every other selection.
	env = append(env, "FIXTURE_REQUIRE_PARALLELISM_CAP=1")
	out, killer, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, "^(TestWeak|TestOracleEnvHasParallelismCap)$", 120*time.Second, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil {
		t.Fatalf("exiting mutant aborted the campaign: %v", err)
	}
	if out != MutantKilled || killer != PackageKillerPrefix+"example.com/fixture/lib)" {
		t.Fatalf("outcome = %v (killer %q), want the package-sentinel kill: a noise discard means the baseline probe ran uncapped", out, killer)
	}

	// Negative arm: with no cap installed the armed sentinel fails on
	// the baseline, so the same mutant reads as environmental noise -
	// proving the sentinel runs (not skips) on the baseline probe and
	// that the positive arm's sentinel kill really discriminated the
	// baseline's environment.
	oracleParallelWidth.Store(0)
	out, killer, _, incomplete, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, "^(TestWeak|TestOracleEnvHasParallelismCap)$", 120*time.Second, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil {
		t.Fatalf("uncapped arm aborted the campaign: %v", err)
	}
	if out != MutantDiscarded || killer != "" || !strings.Contains(incomplete, "baseline probe failed alongside the mutant") {
		t.Fatalf("uncapped arm = %v (killer %q, incomplete %q), want a noise discard from the failing sentinel", out, killer, incomplete)
	}
}

// An observed run covers one test package per process: a
// multi-package request with observation enabled refuses instead of
// silently ingesting only the last binary's truncated testlog as a
// completed observation (REQ-exec-observation).
func TestObservedRunRefusesMultiplePackages(t *testing.T) {
	_, _, _, _, _, err := RunMutantObservedEnv(context.Background(), ".", Mutant{},
		[]string{"example.com/a", "example.com/b"}, ".", time.Minute, nil, "/m", "/m/p", nil, nil, []string{"A=1"})
	if err == nil || !strings.Contains(err.Error(), "one test package per process") {
		t.Fatalf("multi-package observed run = %v, want the refusal", err)
	}
}
