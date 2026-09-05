package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A baseline probe whose test package fails to build refuses with the
// compiler's own diagnostic in the error (REQ-exec-ephemeral).
func TestProbeBuildFailureNamesCompilerDiagnostic(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/broken\n\ngo 1.24\n",
		"lib.go":      "package broken\n\nfunc Value() int { return 1 }\n",
		"lib_test.go": "package broken\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Undefined() != 1 { t.Fail() } }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, err := TestProbe(context.Background(), dir, "example.com/broken", "^TestValue$", time.Minute, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to build") {
		t.Fatalf("broken test package probe = %v, want a build refusal", err)
	}
	if !strings.Contains(err.Error(), "undefined") {
		t.Fatalf("build refusal lacks the compiler diagnostic: %v", err)
	}
}

// compileDiagnostics keeps the compiler's text from both streams — raw
// stderr, build-output events, and non-JSON lines in the -json stream —
// and caps a pathological diagnostic.
func TestCompileDiagnosticsExtraction(t *testing.T) {
	stdout := []byte(`{"Action":"start","Package":"p"}
{"Action":"build-output","Output":"p/f.go:3:2: undefined: q\n"}
# plain interleaved line
{"Action":"fail","Package":"p"}`)
	stderr := []byte("# example.com/p\nvet: something\n")
	got := compileDiagnostics(stdout, stderr)
	for _, want := range []string{"undefined: q", "# plain interleaved line", "# example.com/p", "vet: something"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, `"Action"`) {
		t.Fatalf("diagnostics leak raw JSON events: %q", got)
	}
	long := compileDiagnostics(nil, []byte(strings.Repeat("x", 10000)))
	if len(long) > 5000 || !strings.Contains(long, "[diagnostic truncated]") {
		t.Fatalf("diagnostic cap missing: len=%d", len(long))
	}
}

// Each mutant executes exactly once: the observation bracket binds the
// run's values, so no discovery-then-score double execution exists
// (REQ-exec-observation's exactly-once sentence).
func TestRunMutantExecutesExactlyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/counting.Value", 0)
	if err != nil || len(ms) == 0 {
		t.Fatalf("Mutants: %v", err)
	}
	pick := slices.IndexFunc(ms, func(m Mutant) bool { return m.Operator == "return: zero" })
	if pick < 0 {
		t.Fatalf("no compilable mutant candidate: %+v", ms)
	}
	moduleDir, packageDir, err := tr.PackageContext("example.com/fixture/counting")
	if err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "executions")
	env := append(GoEnv("testdata/fixturemod"), "GOMUTANT_EXECUTION_COUNTER="+counter)
	out, _, _, _, _, _, err := RunMutantObservedEnv(context.Background(), "testdata/fixturemod", ms[pick],
		[]string{"example.com/fixture/counting"}, "^TestCounting$", time.Minute, nil, moduleDir, packageDir, nil, nil, env)
	if err != nil {
		t.Fatal(err)
	}
	if out != MutantSurvived {
		t.Fatalf("counting mutant = %v, want a survivor under the deliberately weak oracle", out)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("oracle executed %d times, want exactly once", got)
	}
}

// The failing-baseline diagnostic renders what the oracle saw: each
// failing top-level test's name and its own output (subtests folded
// under their parent), a test the process never closed — a crash — as
// a failure with its output, package-level output only when no test's
// output explains the failure, never a passing test's output, and a
// bounded rendering.
func TestFailedTestsDiagnosticRendersWhatTheOracleSaw(t *testing.T) {
	render := func(t *testing.T, stream string) (*testStream, string) {
		t.Helper()
		ts, err := parseTestStream([]byte(stream))
		if err != nil {
			t.Fatal(err)
		}
		return ts, failedTestsDiagnostic(ts)
	}
	t.Run("failing test with a subtest", func(t *testing.T) {
		ts, got := render(t, `{"Action":"run","Test":"TestA"}
{"Action":"output","Test":"TestA","Output":"=== RUN   TestA\n"}
{"Action":"output","Test":"TestA/sub","Output":"    a_test.go:9: sub broke\n"}
{"Action":"fail","Test":"TestA/sub"}
{"Action":"output","Test":"TestA","Output":"--- FAIL: TestA (0.00s)\n"}
{"Action":"fail","Test":"TestA"}
{"Action":"run","Test":"TestB"}
{"Action":"output","Test":"TestB","Output":"--- PASS: TestB (0.00s)\n"}
{"Action":"pass","Test":"TestB"}
{"Action":"output","Output":"FAIL\texample.com/p\t0.01s\n"}
`)
		if ts.ran != 2 || !slices.Equal(ts.failed, []string{"TestA"}) || len(ts.truncated) != 0 {
			t.Fatalf("stream = ran %d failed %v truncated %v", ts.ran, ts.failed, ts.truncated)
		}
		if !strings.HasPrefix(got, "TestA:\n") || !strings.Contains(got, "sub broke") || strings.Contains(got, "TestB") || strings.Contains(got, "example.com/p") {
			t.Fatalf("diagnostic = %q", got)
		}
	})
	t.Run("crash-truncated test beside a passing one", func(t *testing.T) {
		// A goroutine panic aborts the binary: the harness attributes
		// the panic text to the running test and never closes it, and
		// the package reports the failure alone.
		ts, got := render(t, `{"Action":"run","Test":"TestOK"}
{"Action":"output","Test":"TestOK","Output":"--- PASS: TestOK (0.00s)\n"}
{"Action":"pass","Test":"TestOK"}
{"Action":"run","Test":"TestCrash"}
{"Action":"output","Test":"TestCrash","Output":"=== RUN   TestCrash\n"}
{"Action":"output","Test":"TestCrash","Output":"panic: goroutine boom marker\n"}
{"Action":"output","Output":"FAIL\texample.com/p\t0.01s\n"}
{"Action":"fail"}
`)
		if ts.ran != 2 || len(ts.failed) != 0 || !slices.Equal(ts.truncated, []string{"TestCrash"}) {
			t.Fatalf("stream = ran %d failed %v truncated %v", ts.ran, ts.failed, ts.truncated)
		}
		if !strings.HasPrefix(got, "TestCrash:\n") || !strings.Contains(got, "goroutine boom marker") || strings.Contains(got, "TestOK") {
			t.Fatalf("diagnostic = %q", got)
		}
		if ts, _ := render(t, `{"Action":"run","Test":"TestCrash"}
{"Action":"output","Test":"TestCrash","Output":"panic: boom\n"}
`); ts.ran != 1 || !slices.Equal(ts.truncated, []string{"TestCrash"}) {
			t.Fatalf("a lone crash = ran %d truncated %v", ts.ran, ts.truncated)
		}
	})
	t.Run("package-level output when no test explains the failure", func(t *testing.T) {
		_, got := render(t, `{"Action":"run","Test":"TestOK"}
{"Action":"output","Test":"TestOK","Output":"--- PASS: TestOK (0.00s)\n"}
{"Action":"pass","Test":"TestOK"}
{"Action":"output","Output":"panic: boom outside any test\n"}
{"Action":"output","Output":"FAIL\texample.com/p\t0.01s\n"}
`)
		if !strings.Contains(got, "boom outside any test") || strings.Contains(got, "TestOK") {
			t.Fatalf("package-level fallback = %q", got)
		}
	})
	t.Run("a test counts once however many terminal events name it", func(t *testing.T) {
		ts, _ := render(t, `{"Action":"run","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"pass","Test":"TestA"}
{"Action":"fail","Test":"TestA"}
`)
		if ts.ran != 1 || !slices.Equal(ts.failed, []string{"TestA"}) {
			t.Fatalf("duplicate terminal events: ran %d failed %v", ts.ran, ts.failed)
		}
	})
	t.Run("skipped tests did not run", func(t *testing.T) {
		ts, _ := render(t, `{"Action":"run","Test":"TestSkipped"}
{"Action":"skip","Test":"TestSkipped"}
`)
		if ts.ran != 0 || len(ts.truncated) != 0 {
			t.Fatalf("a skipped test counted as run: %+v", ts)
		}
	})
	t.Run("bounded", func(t *testing.T) {
		_, got := render(t, `{"Action":"output","Test":"TestA","Output":"`+strings.Repeat("x", 5000)+`\n"}
{"Action":"fail","Test":"TestA"}
`)
		if !strings.HasSuffix(got, "[diagnostic truncated]") || len(got) > 4096+len("\n[diagnostic truncated]") {
			t.Fatalf("unbounded diagnostic: %d bytes", len(got))
		}
	})
}
