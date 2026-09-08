package gomutant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// pruneUnusedImports drops exactly the imports the mutant no longer
// references whose bound name is known — an alias, or the declared
// name the loaded package imports it under — and leaves everything
// else: a used import, a blank or dot import, an import of unknown
// name (a package the file's package never imported, whose name the
// path cannot vouch for), and a source that does not parse
// (REQ-exec-ephemeral).
func TestPruneUnusedImportsDropsOnlyKnownUnreferencedImports(t *testing.T) {
	names := map[string]string{"fmt": "fmt", "errors": "errors", "gopkg.in/yaml.v3": "yaml"}
	lookup := func(path string) (string, bool) { n, ok := names[path]; return n, ok }
	source := `package p

import (
	"errors"
	"fmt"
	str "strings"
	_ "embed"
	. "math"
	"gopkg.in/yaml.v3"
	"example.com/unknown-name"
)

func F() error { return errors.New(Pi) }
`
	out, pruned := pruneUnusedImports([]byte(source), lookup)
	slices.Sort(pruned)
	if want := []string{"fmt", "gopkg.in/yaml.v3", "strings"}; !slices.Equal(pruned, want) {
		t.Fatalf("pruned = %v, want %v", pruned, want)
	}
	text := string(out)
	for _, kept := range []string{`"errors"`, `_ "embed"`, `. "math"`, `"example.com/unknown-name"`} {
		if !strings.Contains(text, kept) {
			t.Fatalf("pruned source lost %s:\n%s", kept, text)
		}
	}
	for _, gone := range []string{`"fmt"`, `"strings"`, `"gopkg.in/yaml.v3"`} {
		if strings.Contains(text, gone) {
			t.Fatalf("pruned source still imports %s:\n%s", gone, text)
		}
	}
	// A referenced import under its declared name stays even when the
	// path's last element differs from it.
	used := "package p\n\nimport \"gopkg.in/yaml.v3\"\n\nvar _ = yaml.Marshal\n"
	if _, pruned := pruneUnusedImports([]byte(used), lookup); len(pruned) != 0 {
		t.Fatalf("a used import was pruned: %v", pruned)
	}
	// Nothing to prune returns the source untouched, byte for byte.
	clean := "package p\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n"
	if out, pruned := pruneUnusedImports([]byte(clean), lookup); string(out) != clean || pruned != nil {
		t.Fatalf("clean source rewritten: %q %v", out, pruned)
	}
	broken := "package p\n\nimport \"fmt\"\n\nfunc F( {"
	if out, pruned := pruneUnusedImports([]byte(broken), lookup); string(out) != broken || pruned != nil {
		t.Fatalf("unparsable source rewritten: %q %v", out, pruned)
	}
	// The pruned source keeps its build constraint, its directives, and
	// its comments: the rewrite drops the import and nothing else.
	directives := "//go:build linux\n\n// Package p is documented.\npackage p\n\nimport (\n\t_ \"embed\"\n\t\"fmt\"\n)\n\n//go:embed data.txt\nvar data string // the embedded fixture\n\n// F is documented.\nfunc F() string { return data }\n"
	out, pruned = pruneUnusedImports([]byte(directives), lookup)
	if !slices.Equal(pruned, []string{"fmt"}) {
		t.Fatalf("pruned = %v, want fmt alone", pruned)
	}
	for _, kept := range []string{"//go:build linux", "// Package p is documented.", "//go:embed data.txt", "// the embedded fixture", "// F is documented.", "_ \"embed\""} {
		if !strings.Contains(string(out), kept) {
			t.Fatalf("pruned source lost %q:\n%s", kept, out)
		}
	}
	if strings.Contains(string(out), "\"fmt\"") {
		t.Fatalf("fmt survived the prune:\n%s", out)
	}
}

// A deletion probe that strands its guard's only import measures the
// deletion itself: the import is pruned before compiling, the oracle
// kills the guardless mutant, and the result names what was pruned —
// where the stranded import once made the honest probe unwritable
// (REQ-exec-ephemeral).
func TestEphemeralPrunesStrandedImports(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tr, err := Load("internal/engine/testdata/guardimportmod")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join("internal/engine/testdata/guardimportmod", "g", "g.go"))
	if err != nil {
		t.Fatal(err)
	}
	deleted := strings.Replace(string(original), "\tif x < 0 {\n\t\treturn fmt.Errorf(\"negative %d\", x)\n\t}\n", "", 1)
	if deleted == string(original) {
		t.Fatal("fixture edit failed")
	}
	res, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "g/g.go", Mutant: []byte(deleted), TestPkg: "example.com/guardimport/g", Run: "^TestCheck$", OracleTimeout: time.Minute, Runs: 1})
	if err != nil {
		t.Fatalf("guard deletion with a stranded import: %v", err)
	}
	if !res.Killed {
		t.Fatalf("the guardless mutant survived: %+v", res)
	}
	if !slices.Equal(res.PrunedImports, []string{"fmt (g/g.go)"}) {
		t.Fatalf("pruned imports = %v, want fmt named for g/g.go", res.PrunedImports)
	}
	// The digest identifies the caller's spelling, the stranded import
	// included: the same edit re-probed matches its attestation.
	if res.EditDigest != ephemeralEditDigest(tr.dir, []fileReplacement{{File: "g/g.go", Abs: filepath.Join(tr.dir, "g", "g.go"), Source: []byte(deleted)}}, true) {
		t.Fatal("the edit digest moved with the pruning")
	}
}

// A plain survivor over a replacement the probed run never reached is
// a refusal naming the repair, not a verdict; a mutated test file is
// admitted and named, since its verdict is about the test, never the
// code under test (REQ-exec-ephemeral's blind spots).
func TestEphemeralRefusesBlindSpotTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tr := fixtureTree(t)
	res, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "lib/ext_test.go", Mutant: []byte("package lib_test\n"), TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", OracleTimeout: time.Minute, Runs: 1})
	if err != nil {
		t.Fatalf("mutated test file refused: %v", err)
	}
	if !slices.Equal(res.MutatedTests, []string{"lib/ext_test.go"}) {
		t.Fatalf("mutated test not named: %+v", res.MutatedTests)
	}
	linkedIdle, err := os.ReadFile("internal/engine/testdata/fixturemod/genp/gen.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(linkedIdle), "type G struct{}", "type G struct{ X int }", 1)
	if mutated == string(linkedIdle) {
		t.Fatal("fixture edit failed")
	}
	_, err = tr.RunEphemeral(context.Background(), EphemeralRequest{File: "genp/gen.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1})
	if err == nil || !strings.Contains(err.Error(), "never reached genp/gen.go") || !strings.Contains(err.Error(), "mutate the guard's own input") {
		t.Fatalf("unexercised survivor = %v; want the no-verdict refusal naming the repair", err)
	}
}

// CompilerCrashed tells a compiler signal death or panic from a compile
// diagnostic: only the former is retried (REQ-exec-ephemeral).
func TestCompilerCrashedDetectsSignalDeaths(t *testing.T) {
	for diagnostic, want := range map[string]bool{
		"# example.com/p\n/usr/lib/go/pkg/tool/linux_amd64/compile: signal: segmentation fault (core dumped)":        true,
		"panic: runtime error: invalid memory address\n\ngoroutine 1 [running]:\ncmd/compile/internal/ir.Visit(...)": true,
		// A crash head truncated to its goroutine dump names no signal
		// and no panic line, only the compiler's frames.
		"goroutine 1 [running]:\ncmd/compile/internal/ir.Visit(...)\n\t/usr/lib/go/src/cmd/compile/internal/ir/visit.go:100": true,
		"# example.com/p\n./p.go:12:2: undefined: fmt":                                        false,
		"# example.com/p\n./p.go:3:8: \"fmt\" imported and not used":                          false,
		"panic: the TEST binary panicked\n\ngoroutine 7 [running]:\nexample.com/p.TestX(...)": false,
	} {
		if got := engine.CompilerCrashed(diagnostic); got != want {
			t.Errorf("CompilerCrashed(%q) = %v, want %v", diagnostic, got, want)
		}
	}
	// The baseline's build failure is judged by its diagnostic, never a
	// failing test's own output.
	crash := &engine.BaselineBuildError{Diagnostic: "# example.com/p\n/usr/lib/go/pkg/tool/linux_amd64/compile: signal: segmentation fault"}
	if !baselineCompilerCrashed(crash) || baselineCompilerCrashed(&engine.BaselineBuildError{Diagnostic: "./p.go:1:1: undefined: x"}) || baselineCompilerCrashed(errors.New("cmd/compile signal: failing test output")) {
		t.Fatal("baselineCompilerCrashed judges the wrong error shape")
	}
}

// The baseline probe's compiler crash is retried once through the
// build-failure path the engine reports it on, and recurring is
// reported as the crash — never as the baseline failing to build; a
// failing test whose output happens to look like a crash is a failing
// test (REQ-exec-ephemeral).
func TestEphemeralRetriesABaselineCompilerCrashOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the baseline probe over a fixture module")
	}
	tr := fixtureTree(t)
	inside, err := os.ReadFile("internal/engine/testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(inside), "return x - 1", "return x - 2", 1)
	crash := &engine.BaselineBuildError{Diagnostic: "# example.com/fixture/lib\n/usr/lib/go/pkg/tool/linux_amd64/compile: signal: segmentation fault (core dumped)"}
	restore := testProbe
	defer func() { testProbe = restore }()
	calls := 0
	testProbe = func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (int, bool, string, error) {
		calls++
		if calls == 1 {
			return 0, false, "", crash
		}
		return restore(ctx, dir, testPkg, run, timeout, binFlags, env, bounds)
	}
	req := EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1}
	res, err := tr.RunEphemeral(context.Background(), req)
	if err != nil {
		t.Fatalf("a transient baseline compiler crash read as a refusal: %v", err)
	}
	if calls != 2 || res.Killed || res.Runs != 1 {
		t.Fatalf("baseline probed %d times, result %+v; want the crash retried once and the measured survivor", calls, res)
	}
	calls = 0
	testProbe = func(context.Context, string, string, string, time.Duration, []string, []string, engine.OracleBounds) (int, bool, string, error) {
		calls++
		return 0, false, "", crash
	}
	if _, err := tr.RunEphemeral(context.Background(), req); err == nil || !strings.Contains(err.Error(), "compiler crashed twice on the baseline") || calls != 2 {
		t.Fatalf("two baseline crashes = %v after %d probes; want the crash named after one retry", err, calls)
	}
	// A baseline test that FAILS printing crash-shaped output is a
	// failing test: no retry, the ordinary refusal.
	calls = 0
	testProbe = func(context.Context, string, string, string, time.Duration, []string, []string, engine.OracleBounds) (int, bool, string, error) {
		calls++
		return 1, false, "--- FAIL: TestWeak\n    cmd/compile panic: signal: goroutine dump quoted by the test", nil
	}
	if _, err := tr.RunEphemeral(context.Background(), req); err == nil || !strings.Contains(err.Error(), "does not pass on the unmutated tree") || calls != 1 {
		t.Fatalf("crash-shaped failing test = %v after %d probes; want the failing-baseline refusal without a retry", err, calls)
	}
}

// The mixed killed-some-runs outcome over a never-reached replacement
// keeps the unexercised advisory where the plain survivor refuses
// (REQ-exec-ephemeral). The kill is planted through the runner seam:
// a genuinely killing run has by definition reached the file, so no
// fixture is both mixed and unreached — the test pins the branch.
func TestEphemeralMixedOutcomeKeepsTheUnexercisedAdvisory(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the baseline probe over a fixture module")
	}
	tr := fixtureTree(t)
	linkedIdle, err := os.ReadFile("internal/engine/testdata/fixturemod/genp/gen.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(linkedIdle), "type G struct{}", "type G struct{ X int }", 1)
	restore := runMutantEvidence
	defer func() { runMutantEvidence = restore }()
	calls := 0
	runMutantEvidence = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		calls++
		if calls == 1 {
			return engine.MutantKilled, "TestWeak", "planted kill", "", nil
		}
		return restore(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, env, bounds)
	}
	res, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "genp/gen.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 2})
	if err != nil {
		t.Fatalf("mixed outcome over an unreached file refused: %v", err)
	}
	if res.Killed || res.KilledRuns != 1 || !slices.Equal(res.UnexercisedFiles, []string{"genp/gen.go"}) {
		t.Fatalf("mixed outcome = %+v; want one killed run and the unexercised advisory", res)
	}
}

// A compiler signal death under a probe is retried once and never
// reads as a verdict or as a mutant that does not compile: a transient
// measures on the retry, and a second death names itself
// (REQ-exec-ephemeral).
func TestEphemeralRetriesACompilerCrashOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the baseline probe over a fixture module")
	}
	tr := fixtureTree(t)
	inside, err := os.ReadFile("internal/engine/testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(inside), "return x - 1", "return x - 2", 1)
	if mutated == string(inside) {
		t.Fatal("fixture edit failed")
	}
	const crash = "# example.com/fixture/lib\n/usr/lib/go/pkg/tool/linux_amd64/compile: signal: segmentation fault (core dumped)"
	restore := runMutantEvidence
	defer func() { runMutantEvidence = restore }()
	calls := 0
	runMutantEvidence = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		calls++
		if calls == 1 {
			return engine.MutantDiscarded, "", "", crash, nil
		}
		return restore(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, env, bounds)
	}
	req := EphemeralRequest{File: "lib/lib.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1}
	res, err := tr.RunEphemeral(context.Background(), req)
	if err != nil {
		t.Fatalf("a transient compiler crash read as a refusal: %v", err)
	}
	if calls != 2 || res.Killed {
		t.Fatalf("calls = %d, result = %+v; want one retry and the measured survivor", calls, res)
	}
	calls = 0
	runMutantEvidence = func(context.Context, string, engine.Mutant, []string, string, time.Duration, []string, []string, engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		calls++
		return engine.MutantDiscarded, "", "", crash, nil
	}
	if _, err := tr.RunEphemeral(context.Background(), req); err == nil || !strings.Contains(err.Error(), "compiler crashed twice") || strings.Contains(err.Error(), "did not compile") {
		t.Fatalf("two crashes = %v; want the crash named, never a compile failure", err)
	}
	if calls != 2 {
		t.Fatalf("crash retried %d times, want exactly once", calls-1)
	}
	// A genuine compile diagnostic is not retried.
	calls = 0
	runMutantEvidence = func(context.Context, string, engine.Mutant, []string, string, time.Duration, []string, []string, engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		calls++
		return engine.MutantDiscarded, "", "", "# example.com/fixture/lib\n./lib.go:9:2: undefined: nope", nil
	}
	if _, err := tr.RunEphemeral(context.Background(), req); err == nil || !strings.Contains(err.Error(), "did not compile") || calls != 1 {
		t.Fatalf("compile diagnostic = %v after %d calls; want the compile refusal without a retry", err, calls)
	}
}
