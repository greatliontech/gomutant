package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// A self-reexecuted test binary judges the linked mutant, but its counters
// are not in the parent's coverprofile. An in-process weak batch DOES reach
// the same extent, so the none-reaching fallback cannot rescue this oracle.
func TestCoverageAuthorityChildProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real coverage probes and mutant oracles")
	}
	coverageAuthorityRegression(t, coverageAuthorityArithmetic, "7", `
import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func weak() { _ = Value(0) }

func decide(t *testing.T) {
	if os.Getenv("COVERAGE_AUTHORITY_CHILD") == "1" {
		if got := Value(0); got != 90 {
			t.Fatalf("child linked value = %d, want 90", got)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+t.Name()+"$", "-test.timeout=20s")
	cmd.WaitDelay = time.Second
	// The child has its own harness and no parent profile destination.
	// It still runs the deciding assertion against the same linked binary.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "COVERAGE_AUTHORITY_CHILD=") && !strings.HasPrefix(entry, "GOCOVERDIR=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "COVERAGE_AUTHORITY_CHILD=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil { t.Fatalf("child exceeded its bound: %v: %s", ctx.Err(), out) }
	if err != nil { t.Fatalf("child rejected linked value: %v: %s", err, out) }
}
`)
}

// Coverage runs and scored runs have different GOCOVERDIR values. Every
// deciding test asserts its result in BOTH environments, but the coverage
// branch obtains the result without calling the mutable implementation.
func TestCoverageAuthorityCoverdirEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real coverage probes and mutant oracles")
	}
	coverageAuthorityRegression(t, coverageAuthorityArithmetic, "7", `
import (
	"os"
	"testing"
)

func weak() { _ = Value(0) }

func result() int {
	if os.Getenv("GOCOVERDIR") != "" { return 90 }
	return Value(0)
}

func decide(t *testing.T) {
	if got := result(); got != 90 {
		t.Fatalf("environment-selected value = %d, want 90", got)
	}
}
`)
}

// A goto may jump over a constant declaration: unlike a variable, its
// value is available at the destination without executing the declaration's
// coverage block. Mutating that value changes the deciding test's result.
func TestCoverageAuthorityGotoConstant(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real coverage probes and mutant oracles")
	}
	coverageAuthorityRegression(t, `package coverageauthority

func Value(weak bool) int {
	if !weak { goto use }
	const value = 7
	return value
use:
	return value
}
`, "7", `
import "testing"

func weak() { _ = Value(true) }

func decide(t *testing.T) {
	if got := Value(false); got != 7 {
		t.Fatalf("constant used beyond goto = %d, want 7", got)
	}
}
`)
}

const coverageAuthorityPackage = "example.com/coverageauthority"

const coverageAuthorityArithmetic = `package coverageauthority

func Value(x int) int {
	return x + 7 + 11 + 13 + 17 + 19 + 23
}
`

// All fixture inputs and outputs live in a fresh temporary module. In
// particular no build, profile, child process, or overlay writes into the
// tracked fixture tree that the root suite's fixtureguard protects.
func coverageAuthorityRegression(t *testing.T, source, literal, testBody string) {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOCOVERDIR", "")
	t.Setenv("COVERAGE_AUTHORITY_CHILD", "")
	dir := t.TempDir()
	var testSource strings.Builder
	testSource.WriteString("package coverageauthority\n" + testBody)
	var tests []string
	// Nine tests give three batches naturally at the production threshold:
	// one reaching weak batch, followed by two non-reaching deciding batches.
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("TestAWeak%d", i)
		tests = append(tests, name)
		fmt.Fprintf(&testSource, "\nfunc %s(t *testing.T) { weak() }\n", name)
	}
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("TestZDecides%d", i)
		tests = append(tests, name)
		fmt.Fprintf(&testSource, "\nfunc %s(t *testing.T) { decide(t) }\n", name)
	}
	for name, content := range map[string]string{
		"go.mod":    "module " + coverageAuthorityPackage + "\n\ngo 1.26\n",
		"p.go":      source,
		"p_test.go": testSource.String(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	tr, err := LoadContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	symbol := coverageAuthorityPackage + ".Value"
	generation, err := tr.eng.CandidatesContext(ctx, symbol, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Locate the real catalog candidate that changes the first literal.
	// No replacement, extent, coverage, or verdict is fabricated by this pin.
	offset := strings.Index(source, literal)
	if offset < 0 {
		t.Fatal("fixture literal absent")
	}
	line := 1 + strings.Count(source[:offset], "\n")
	col := offset - strings.LastIndex(source[:offset], "\n")
	position := fmt.Sprintf("p.go:%d:%d", line, col)
	var mutant engine.Mutant
	for _, candidate := range generation.Candidates {
		if candidate.Position == position && candidate.Operator == "integer literal: magnitude +1" {
			mutant, _ = candidate.Mutant()
		}
	}
	if len(mutant.Replacements) == 0 {
		t.Fatalf("catalog has no literal mutation at %s: %+v", position, generation.Candidates)
	}
	env := os.Environ()
	bounds := engine.DeriveOracleBounds(-1, 1)
	const oracleBound = 2 * time.Minute
	full := testRunRegex(tests)
	ran, pass, diagnostic, err := engine.TestProbeEnv(ctx, dir, coverageAuthorityPackage, full, oracleBound, nil, env, bounds)
	if err != nil || !pass || ran != len(tests) {
		t.Fatalf("full unmutated oracle: ran=%d pass=%v err=%v\n%s", ran, pass, err, diagnostic)
	}
	entry := bankedCoverage{Plan: len(scheduleBatches(tests))}
	probe := Survivor{Position: mutant.Position, Extent: mutant.Extent}
	for i, batch := range scheduleBatches(tests) {
		start := time.Now()
		cov, err := engine.CoveredPositions(ctx, dir, coverageAuthorityPackage, testRunRegex(batch), coverageAuthorityPackage, oracleBound, nil, env, tr.eng.DirectiveCoverage(), bounds)
		if err != nil {
			t.Fatalf("real coverage batch %d: %v", i, err)
		}
		reached, usable := survivorCovered(cov, coverageAuthorityPackage, probe)
		if !usable || reached != (i == 0) {
			t.Fatalf("batch %v: reached=%v usable=%v at %s/%s; want only the weak batch reaching", batch, reached, usable, mutant.Position, mutant.Extent)
		}
		entry.Batches = append(entry.Batches, bankedBatch{Index: i, Fns: batch, Coverage: cov.Persist(), DurMillis: time.Since(start).Milliseconds()})
	}
	t.Logf("measured mixed coverage: weak reaches %s/%s; both deciding batches miss", mutant.Position, mutant.Extent)
	// The positive control comes from real mutated executions, independent
	// of any interpretation of the parent profile's negative counts.
	for _, control := range []struct {
		name, pattern string
		want          engine.MutantOutcome
	}{
		{"weak assertions pass", testRunRegex(tests[:3]), engine.MutantSurvived},
		{"full oracle kills", full, engine.MutantKilled},
	} {
		outcome, killer, _, _, _, detail, err := engine.RunMutantObservedEnv(ctx, dir, mutant, []string{coverageAuthorityPackage}, control.pattern, oracleBound, nil, dir, dir, nil, nil, env, bounds)
		if err != nil || outcome != control.want {
			t.Fatalf("%s: outcome=%v killer=%q err=%v\n%s", control.name, outcome, killer, err, detail)
		}
		if outcome == engine.MutantKilled && !strings.HasPrefix(killer, coverageAuthorityPackage+".TestZDecides") {
			t.Fatalf("full oracle kill attributed to %q, want an actual deciding test", killer)
		}
		t.Logf("%s: outcome=%v killer=%q", control.name, outcome, killer)
	}
	t.Run("schedule_keeps_every_oracle_test", func(t *testing.T) {
		w := scheduleTestWork(coverageAuthorityPackage, coverageAuthorityPackage, tests)
		bank := openBaselineBank(dir)
		bank.putCoverage(coverageKey(w.groups[0], coverageAuthorityPackage), entry)
		out, killer, _, _, _, err := tr.executeMutant(ctx, w, mutant, runOptions{Options: Options{OracleTimeout: oracleBound}, baselineBank: bank, bounds: bounds}, env)
		if err != nil || out != engine.MutantKilled || !strings.HasPrefix(killer, coverageAuthorityPackage+".TestZDecides") {
			t.Fatalf("negative parent coverage removed deciding tests: outcome=%v killer=%q err=%v", out, killer, err)
		}
	})
	assertPipeline := func(t *testing.T) {
		// Delegate unchanged to the real executor. Record the FIRST execution
		// for each candidate so an audit's sampled correction cannot satisfy
		// the assertion, and killer-scoped serial confirmations remain free.
		original := seams.runMutantObserved
		var mu sync.Mutex
		first := map[string]string{}
		seams.runMutantObserved = func(ctx context.Context, dir string, m engine.Mutant, pkgs []string, pattern string, timeout time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
			mu.Lock()
			key := m.Position + " " + m.Operator
			if _, ok := first[key]; !ok {
				first[key] = pattern
			}
			mu.Unlock()
			return original(ctx, dir, m, pkgs, pattern, timeout, flags, moduleDir, packageDir, brackets, namespaces, env, bounds)
		}
		t.Cleanup(func() { seams.runMutantObserved = original })
		var oracle []string
		for _, name := range tests {
			oracle = append(oracle, coverageAuthorityPackage+"."+name)
		}
		findings, err := tr.Run(ctx, []Target{{Symbol: symbol, Oracle: oracle}}, Options{Jobs: 1, OracleTimeout: oracleBound, AnalysisBudget: time.Second, OracleMemoryBytes: -1})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 1 || findings[0].Mutants == 0 {
			t.Fatalf("pipeline measured no mutants: %+v", findings)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(first) < 2 {
			t.Fatalf("only %d candidate executions observed; cannot distinguish an audit sample", len(first))
		}
		omitted := 0
		for _, candidate := range generation.Candidates {
			if _, runnable := candidate.Mutant(); !runnable {
				continue
			}
			key := candidate.Position + " " + candidate.Operator
			pattern, ok := first[key]
			if !ok {
				t.Errorf("catalog candidate %s never executed", key)
				continue
			}
			rx, err := regexp.Compile(pattern)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range tests {
				if !rx.MatchString(name) {
					omitted++
					t.Errorf("candidate %s first ran %q, omitting oracle test %s", key, pattern, name)
					break
				}
			}
		}
		f := findings[0]
		if f.OracleExecutionPolicy != FullOracleExecutionPolicy {
			t.Errorf("fresh execution policy=%q", f.OracleExecutionPolicy)
		}
		t.Logf("pipeline: %d candidates executed, %d first executions omitted tests; mutants=%d killed=%d survivors=%d discarded=%d", len(first), omitted, f.Mutants, f.Killed, len(f.Survivors), f.Discarded)
		killed := false
		for _, kill := range f.Kills {
			if kill.Position == mutant.Position && kill.Operator == mutant.Operator && strings.HasPrefix(kill.Killer, coverageAuthorityPackage+".TestZDecides") {
				killed = true
			}
		}
		if !killed {
			t.Errorf("pipeline failed to retain the full-oracle control's literal kill at %s: kills=%+v survivors=%+v", mutant.Position, f.Kills, f.Survivors)
		}
	}
	t.Run("full_oracle_pipeline_control", func(t *testing.T) {
		// An empty bank is the independent full-pipeline control. The other
		// arm retains the real mixed-coverage bank planted above.
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		assertPipeline(t)
	})
	t.Run("pipeline_executes_full_oracle_for_every_candidate", assertPipeline)
}
