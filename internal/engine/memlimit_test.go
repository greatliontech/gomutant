package engine

import (
	"context"
	"os"
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
	SetOracleMemoryLimit(-1, 1)
	if env := oracleMemoryEnv([]string{"A=1"}); len(env) != 1 {
		t.Fatalf("disabled ceiling touched the env: %v", env)
	}
	SetOracleMemoryLimit(1000, 1)
	t.Cleanup(func() { SetOracleMemoryLimit(-1, 1) })
	env := oracleMemoryEnv([]string{"A=1"})
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

	SetOracleMemoryLimit(256<<20, 1)
	t.Cleanup(func() { SetOracleMemoryLimit(-1, 1) })
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	m := Mutant{
		Symbol: seed.Symbol, Operator: "hand: runaway allocation", Position: seed.Position,
		Replacements: []Replacement{{File: seed.Replacements[0].File, Source: []byte(runaway)}},
	}
	start := time.Now()
	out, killer, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", m,
		[]string{"example.com/fixture/lib"}, "^TestWeak$", 120*time.Second, nil, moduleDir, packageDir, nil, GoEnv("testdata/fixturemod"))
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
	out, killer, _, _, _, err = RunMutantObservedEnv(context.Background(), "testdata/fixturemod", senseMutant,
		[]string{"example.com/fixture/lib"}, "^TestWeak$", 120*time.Second, nil, moduleDir, packageDir, nil, sensingEnv)
	if err != nil {
		t.Fatalf("env-sensing mutant aborted the campaign: %v", err)
	}
	if out != MutantSurvived {
		t.Fatalf("env-sensing mutant outcome = %v (killer %q): GOMEMLIMIT did not reach the oracle environment", out, killer)
	}
}
