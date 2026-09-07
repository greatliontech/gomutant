package engine

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The derived default is RAM/(2 x jobs) floored at 1 GiB; an unreadable
// total disables rather than guesses (REQ-exec-oracle-memory).
func TestDefaultOracleMemoryLimit(t *testing.T) {
	total := totalRAMBytes()
	if total <= 0 {
		t.Skip("total RAM unreadable on this host")
	}
	want := total / 8
	if want < memoryFloorBytes {
		want = memoryFloorBytes
	}
	if got := DefaultOracleMemoryLimit(4); got != want {
		t.Fatalf("DefaultOracleMemoryLimit(4) = %d, want %d", got, want)
	}
	if got, one := DefaultOracleMemoryLimit(0), DefaultOracleMemoryLimit(1); got != one {
		t.Fatalf("jobs floor: %d vs %d", got, one)
	}
	huge := DefaultOracleMemoryLimit(1 << 30)
	if huge != memoryFloorBytes {
		t.Fatalf("floor = %d, want %d", huge, memoryFloorBytes)
	}
}

// The soft ceiling rides the oracle environment at ~90% of the hard
// cap; a disabled ceiling leaves the environment untouched.
func TestOracleMemoryEnv(t *testing.T) {
	if env := oracleMemoryEnv([]string{"A=1"}, DeriveOracleBounds(-1, 1).MemoryBytes); len(env) != 1 {
		t.Fatalf("disabled ceiling touched the env: %v", env)
	}
	env := oracleMemoryEnv([]string{"A=1"}, 1000)
	if len(env) != 2 || env[1] != "GOMEMLIMIT=900" {
		t.Fatalf("soft ceiling = %v, want GOMEMLIMIT=900 appended", env)
	}
}

// A runaway-allocation mutant dies on its own ceiling as an ordinary
// kill, quickly, instead of exhausting the host until the kernel OOM
// killer fires - the field report's shape, contained
// (REQ-exec-oracle-memory).
func TestOracleMemoryCeilingContainsRunawayMutant(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant")
	}
	if !memoryCeilingSupported {
		t.Skip("no hard-cap mechanism on this platform")
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
	const runawayBody = "func Weak(x int) int {\n\tvar hoard [][]byte\n\tfor {\n\t\thoard = append(hoard, make([]byte, 1<<20))\n\t}\n}"
	if !strings.Contains(string(original), weakBody) {
		t.Fatal("fixture Weak body moved; update the runaway replacement")
	}
	runaway := strings.Replace(string(original), weakBody, runawayBody, 1)

	bounds := DeriveOracleBounds(256<<20, 1)
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	m := Mutant{
		Symbol: seed.Symbol, Operator: "hand: runaway allocation", Position: seed.Position,
		Replacements: []Replacement{{File: seed.Replacements[0].File, Source: []byte(runaway)}},
	}
	start := time.Now()
	out, killer, memoryDecided, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, "^TestWeak$", 120*time.Second, nil, moduleDir, packageDir, nil, nil, GoEnv("testdata/fixturemod"), bounds)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runaway mutant aborted the campaign: %v", err)
	}
	if out != MutantKilled {
		t.Fatalf("runaway mutant outcome = %v (killer %q), want killed by its ceiling", out, killer)
	}
	if elapsed > 90*time.Second {
		t.Fatalf("containment took %v - the ceiling did not fire", elapsed)
	}
	// The verdict was authored by the ceiling: the record must say so,
	// or it would serve directionally under a larger ceiling where the
	// runaway might survive (REQ-exec-oracle-memory).
	if !memoryDecided {
		t.Fatalf("ceiling-decided kill not attributed to memory (killer %q)", killer)
	}

	// The soft limit genuinely rides the oracle environment: this mutant
	// runs away ONLY when GOMEMLIMIT is absent, so it survives fast when
	// the env reached the oracle and dies on the hard cap when it did
	// not - pinning the per-site env wiring, not just the composer.
	sensingBody := "func Weak(x int) int {\n" +
		"\tif os.Getenv(\"GOMEMLIMIT\") == \"\" {\n" +
		"\t\tvar hoard [][]byte\n" +
		"\t\tfor {\n" +
		"\t\t\thoard = append(hoard, make([]byte, 1<<20))\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn x\n" +
		"}"
	sensing := strings.Replace(string(original), weakBody, sensingBody, 1)
	sensing = strings.Replace(sensing, "package lib\n", "package lib\n\nimport \"os\"\n", 1)
	senseMutant := Mutant{
		Symbol: seed.Symbol, Operator: "hand: env-sensing runaway", Position: seed.Position,
		Replacements: []Replacement{{File: seed.Replacements[0].File, Source: []byte(sensing)}},
	}
	sensingEnv := make([]string, 0, len(GoEnv("testdata/fixturemod")))
	for _, kv := range GoEnv("testdata/fixturemod") {
		// Ambient GOMEMLIMIT would make the sensing arm vacuous: the
		// mutant must see only what the ceiling wiring injects.
		if !strings.HasPrefix(kv, "GOMEMLIMIT=") {
			sensingEnv = append(sensingEnv, kv)
		}
	}
	out, killer, _, _, _, _, err = RunMutantObservedEnv(context.Background(), "testdata/fixturemod", senseMutant,
		[]string{"example.com/fixture/lib"}, "^TestWeak$", 120*time.Second, nil, moduleDir, packageDir, nil, nil, sensingEnv, bounds)
	if err != nil {
		t.Fatalf("env-sensing mutant aborted the campaign: %v", err)
	}
	if out != MutantSurvived {
		t.Fatalf("env-sensing mutant outcome = %v (killer %q): GOMEMLIMIT did not reach the oracle environment", out, killer)
	}
}

// The memory-death signature set: Go runtime fatals and ENOMEM error
// text mark a memory-decided verdict; ordinary failure output does not
// (REQ-exec-oracle-memory). Overcatching is the sound direction.
func TestMemoryDecidedKillSignatures(t *testing.T) {
	for _, decided := range []string{
		"fatal error: runtime: out of memory",
		"mmap: out of memory allocating heap arena",
		"fork/exec /bin/true: cannot allocate memory",
	} {
		if !memoryDecidedKill([]byte("prefix\n" + decided + "\nsuffix")) {
			t.Errorf("signature not recognized: %q", decided)
		}
	}
	for _, clean := range []string{
		"--- FAIL: TestWeak (0.01s)",
		"expected 3, got 4",
		"panic: runtime error: index out of range",
	} {
		if memoryDecidedKill([]byte(clean)) {
			t.Errorf("ordinary failure misattributed to memory: %q", clean)
		}
	}
}

// The bounds a run derives: an explicit ceiling is taken as given, a
// zero choice derives the default, a negative one disables; the width
// is a lone tree's at jobs=1 and narrows with the job count.
func TestDeriveOracleBounds(t *testing.T) {
	if b := DeriveOracleBounds(3<<30, 2); b.MemoryBytes != 3<<30 || b.Width != oracleParallelismWidth(2) {
		t.Fatalf("explicit = %+v", b)
	}
	if b := DeriveOracleBounds(0, 1); b.MemoryBytes != DefaultOracleMemoryLimit(1) || b.Width != runtime.NumCPU() {
		t.Fatalf("derived = %+v", b)
	}
	if b := DeriveOracleBounds(-1, 1); b.MemoryBytes != 0 {
		t.Fatalf("disabled = %+v", b)
	}
}

// The ceiling reaches the BASELINE probe exactly as it reaches the
// mutant: the two spawns share the run's one bounds value, so a kill
// is never a ceiling artifact of one side (REQ-exec-oracle-memory,
// REQ-exec-attribution-symmetry). The fixture's armed sentinel fails
// on an unceilinged spawn: with the ceiling on both sides the exiting
// mutant is the package-sentinel kill; with no ceiling the sentinel
// fails on the baseline too and the mutant reads as noise.
func TestOracleMemoryCeilingReachesTheBaselineProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
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
		if !strings.HasPrefix(kv, "GOMEMLIMIT=") {
			env = append(env, kv)
		}
	}
	env = append(env, "FIXTURE_REQUIRE_MEMORY_CEILING=1")
	const pattern = "^(TestWeak|TestOracleEnvHasMemoryCeiling)$"
	out, killer, _, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, pattern, 120*time.Second, nil, moduleDir, packageDir, nil, nil, env, DeriveOracleBounds(2<<30, 1))
	if err != nil {
		t.Fatalf("exiting mutant aborted the campaign: %v", err)
	}
	if out != MutantKilled || killer != PackageKillerPrefix+"example.com/fixture/lib)" {
		t.Fatalf("outcome = %v (killer %q), want the package-sentinel kill: a noise discard means the baseline probe ran without the ceiling", out, killer)
	}
	out, killer, _, _, incomplete, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, pattern, 120*time.Second, nil, moduleDir, packageDir, nil, nil, env, OracleBounds{})
	if err != nil {
		t.Fatalf("unbounded arm aborted the campaign: %v", err)
	}
	if out != MutantDiscarded || killer != "" || !strings.Contains(incomplete, "baseline probe failed alongside the mutant") {
		t.Fatalf("unbounded arm = %v (killer %q, incomplete %q), want a noise discard from the failing sentinel", out, killer, incomplete)
	}
}
