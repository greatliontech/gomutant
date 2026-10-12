package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// TestNamedKillerNeedsItsOwnBaselinePass drives both deciding surfaces with a
// passing sibling beside a skipped test which would fail only under mutation.
// The latter has no passed-baseline authority despite matching the full pattern.
func TestNamedKillerNeedsItsOwnBaselinePass(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real baselines and mutants")
	}
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOENV", "off")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, skipped := range []bool{true, false} {
		t.Run(map[bool]string{true: "skipped", false: "passed"}[skipped], func(t *testing.T) {
			dir := t.TempDir()
			const pkg = "example.com/baselinepass"
			original := "package p\nfunc Value() int { return 0 }\n"
			zero := "return"
			if skipped {
				zero = "t.Skip(\"not applicable on the original\")"
			}
			for name, text := range map[string]string{
				"go.mod":    "module " + pkg + "\n\ngo 1.27.0\n",
				"p.go":      original,
				"p_test.go": "package p\nimport \"testing\"\nfunc TestWeak(t *testing.T) { _ = Value() }\nfunc TestConditional(t *testing.T) { if Value() == 0 { " + zero + "; return }; t.Fatal(\"changed\") }\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			tr, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			res, err := tr.RunEphemeral(ctx, EphemeralRequest{File: "p.go", Mutant: []byte(strings.Replace(original, "return 0", "return 1", 1)), TestPkg: pkg, Run: "^(TestWeak|TestConditional)$", OracleTimeout: time.Minute, Runs: 1})
			if skipped {
				if err == nil || !strings.Contains(err.Error(), "did not pass in the pre-measurement baseline") || res != nil {
					t.Fatalf("ephemeral admitted skipped killer: result=%+v error=%v", res, err)
				}
			} else if err != nil || !res.Killed || res.Killer != pkg+".TestConditional" {
				t.Fatalf("passed killer refused: result=%+v error=%v", res, err)
			}
			commits := 0
			findings, err := tr.Run(ctx, []Target{{Symbol: pkg + ".Value", Oracle: []string{pkg + ".TestWeak", pkg + ".TestConditional"}}}, Options{Budget: 0, Jobs: 1, OracleTimeout: time.Minute, Commit: func(Finding) error { commits++; return nil }})
			if skipped {
				if err == nil || !strings.Contains(err.Error(), "did not pass in the pre-measurement baseline") || commits != 0 {
					t.Fatalf("campaign admitted skipped killer: findings=%+v commits=%d error=%v", findings, commits, err)
				}
			} else if err != nil || commits != 1 || len(findings) != 1 || findings[0].Killed == 0 {
				t.Fatalf("campaign refused passed killer: findings=%+v commits=%d error=%v", findings, commits, err)
			}
		})
	}
}

// Active fuzzing supplies a real passing terminal outside -run. Its aggregate
// success cannot supply the skipped killer's own scoped baseline pass.
func TestScopedConfirmationUsesItsOwnNamedBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a scoped baseline with an independent fuzz terminal")
	}
	t.Setenv("GOFLAGS", "-fuzz=FuzzSide -fuzztime=1x -parallel=1")
	t.Setenv("GOENV", "off")
	dir := t.TempDir()
	const pkg = "example.com/scopedpass"
	for name, text := range map[string]string{
		"go.mod": "module " + pkg + "\n\ngo 1.27.0\n",
		"p.go":   "package p\nfunc Value() int { return 0 }\n",
		"p_test.go": `package p
import "testing"
var ready bool
func TestASetup(t *testing.T) { ready = true }
func TestKiller(t *testing.T) {
 if Value() != 0 { t.Fatal("changed") }
 if !ready { t.Skip("requires setup") }
}

func FuzzSide(f *testing.F) { f.Add(0); f.Fuzz(func(t *testing.T, n int) {}) }
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bounds := engine.DeriveOracleBounds(-1, 2)
	baseline, err := engine.TestBaselineEnv(ctx, dir, pkg, "^TestKiller$", time.Minute, nil, tr.eng.GoEnv(), bounds)
	if err != nil || !baseline.Passed || baseline.Ran == 0 || !slices.Contains(baseline.Tests, pkg+".FuzzSide") || slices.Contains(baseline.Tests, pkg+".TestKiller") {
		t.Fatalf("scoped baseline did not exercise the positive aggregate: %+v, %v", baseline, err)
	}
	w := scheduleTestWork(pkg, pkg, []string{"TestASetup", "TestKiller", "FuzzSide"})
	w.groups[0].moduleDir, w.groups[0].packageDir = dir, dir
	full, err := engine.TestBaselineEnv(ctx, dir, pkg, w.groups[0].runRegex, time.Minute, nil, tr.eng.GoEnv(), bounds)
	if err != nil || !full.Passed || !slices.Contains(full.Tests, pkg+".TestKiller") {
		t.Fatalf("full baseline: %+v, %v", full, err)
	}
	w.groups[0].passed = full.Tests
	m := engine.Mutant{Symbol: pkg + ".Value", Replacements: []engine.Replacement{{File: filepath.Join(dir, "p.go"), Source: []byte("package p\nfunc Value() int { return 1 }\n")}}}
	prior := seams.runMutantObserved
	t.Cleanup(func() { seams.runMutantObserved = prior })
	var patterns []string
	seams.runMutantObserved = func(ctx context.Context, dir string, m engine.Mutant, pkgs []string, pattern string, bound time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, pattern)
		return prior(ctx, dir, m, pkgs, pattern, bound, flags, moduleDir, packageDir, brackets, namespaces, env, bounds)
	}
	out, killer, _, _, _, err := tr.confirmMutant(ctx, w, m, pkg+".TestKiller", map[scopedBaselineKey][]string{}, runOptions{Options: Options{OracleTimeout: time.Minute}, bounds: bounds}, tr.eng.GoEnv())
	if err != nil || out != engine.MutantKilled || killer != pkg+".TestKiller" || !slices.Equal(patterns, []string{w.groups[0].runRegex}) {
		t.Fatalf("confirmation borrowed full-group pass: result=%v killer=%q patterns=%v err=%v", out, killer, patterns, err)
	}
}

func TestScopedConfirmationCannotBorrowAnotherTestsPass(t *testing.T) {
	const pkg = "example.com/scope"
	w := scheduleTestWork(pkg, pkg, []string{"TestA", "TestB"})
	w.groups[0].passed = []string{pkg + ".TestA", pkg + ".TestB"}
	priorGround, priorRun := seams.killGround, seams.runMutantObserved
	t.Cleanup(func() { seams.killGround, seams.runMutantObserved = priorGround, priorRun })
	seams.killGround = func(context.Context, string, string, string, time.Duration, []string, string, string, []string, []runtimeinput.ScratchNamespace, []string, engine.OracleBounds) (engine.Baseline, error) {
		return engine.Baseline{Ran: 1, Passed: true, Tests: []string{pkg + ".TestA"}}, nil
	}
	var patterns []string
	seams.runMutantObserved = func(_ context.Context, _ string, _ engine.Mutant, _ []string, pattern string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string, _ engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, pattern)
		if pattern == "^TestA$" {
			return engine.MutantKilled, pkg + ".TestB", false, runtimeinput.Observation{}, "", "", nil
		}
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	out, _, _, _, _, err := (&Tree{}).confirmMutant(context.Background(), w, engine.Mutant{}, pkg+".TestA", map[scopedBaselineKey][]string{}, runOptions{Options: Options{OracleTimeout: time.Minute}}, nil)
	if err != nil || out != engine.MutantSurvived || !slices.Equal(patterns, []string{"^TestA$", w.groups[0].runRegex}) {
		t.Fatalf("unlicensed scoped kill did not fall back: %v, %v, %v", out, patterns, err)
	}
}
