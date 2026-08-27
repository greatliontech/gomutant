package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

func TestEphemeralPreparationCancellation(t *testing.T) {
	tree := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := tree.Ephemeral(ctx, "missing.go", nil, "p", "T", time.Minute, 1); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("cancelled replacement = %+v, %v", result, err)
	}
	if result, err := tree.EphemeralBatch(ctx, []BatchEdit{{File: "missing.go"}}, "p", "T", time.Minute, 1); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("cancelled batch = %+v, %v", result, err)
	}
	if result, err := tree.EphemeralEdits(ctx, "missing.go", []Edit{{Old: "x", New: "y"}}, "p", "T", time.Minute, 1); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("cancelled edits = %+v, %v", result, err)
	}
	if result, err := ApplyEditsContext(ctx, []byte("x"), []Edit{{Old: "x", New: "y"}}); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("cancelled apply = %q, %v", result, err)
	}
}

// TestEphemeral pins the manual-mutant runner (REQ-exec-ephemeral): a
// behavior-breaking replacement is killed with an attributed killer, a
// replacement the test cannot see survives, an identical replacement and a
// zero-match or failing-clean probe refuse the run, and the working tree is
// never touched.
func TestEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("GOMUTANT_FROZEN_INPUT", "loaded")
	tr := fixtureTree(t)
	t.Setenv("GOMUTANT_FROZEN_INPUT", "changed-after-load")
	ctx := context.Background()
	libPath := filepath.Join(fixtureDir, "lib", "lib.go")
	orig, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(fixtureDir, "lib", "doc.go")
	origDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}

	// Breaking Add's tested arm: TestAdd kills, attributed.
	broken := strings.Replace(string(orig), "return a + b", "return a + b + 1", 1)
	res, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("breaking mutant = %+v, want killed by TestAdd", res)
	}
	// The kill carries its own evidence: the killing test's bounded
	// output head, so acting on it needs no parallel oracle re-run.
	if !strings.Contains(res.KillerOutput, "TestAdd") {
		t.Fatalf("kill evidence missing the killing test's output: %q", res.KillerOutput)
	}
	if res.Runs != 1 || res.KilledRuns != 1 || len(res.RunVerdicts) != 1 || res.RunVerdicts[0] != "killed: example.com/fixture/lib.TestAdd" {
		t.Fatalf("single-run verdicts = %+v", res)
	}

	// runs:N - a deterministic kill kills every run: the consecutive-kill
	// claim that splits it from a property generator's draw luck.
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Minute, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Runs != 3 || res.KilledRuns != 3 || len(res.RunVerdicts) != 3 {
		t.Fatalf("three-run deterministic kill = %+v", res)
	}

	// A mutant flaky by construction (kills only when its marker file is
	// absent, and the first kill plants it) reads as neither a
	// deterministic kill nor plain survival: the per-run verdicts carry
	// the mixture.
	// The marker name carries this process's pid: concurrent checkouts
	// running this test share os.TempDir and must not interleave.
	markerName := fmt.Sprintf("gomutant-flaky-probe-marker-%d", os.Getpid())
	marker := filepath.Join(os.TempDir(), markerName)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	flaky, err := tr.EphemeralBatch(ctx, []BatchEdit{
		// The marker path is baked in absolute: each oracle run has its
		// own scratch TMPDIR (REQ-exec-oracle-scratch), so the mutant's
		// cross-run channel must live outside it.
		{File: "lib/doc.go", OldString: "package lib", NewString: "package lib\n\nimport \"os\"\n\nfunc addFlaky(a, b int) int {\n\tmarker := \"" + marker + "\"\n\tif _, err := os.Stat(marker); err != nil {\n\t\t_ = os.WriteFile(marker, []byte(\"x\"), 0o644)\n\t\treturn a + b + 1\n\t}\n\treturn a + b\n}"},
		{File: "lib/lib.go", OldString: "return a + b", NewString: "return addFlaky(a, b)"},
	}, "example.com/fixture/lib", "^TestAdd$", time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if flaky.Killed || flaky.KilledRuns != 1 || flaky.Runs != 2 {
		t.Fatalf("flaky mutant = %+v, want killed 1/2 runs and not the consecutive-kill claim", flaky)
	}
	if len(flaky.RunVerdicts) != 2 || flaky.RunVerdicts[0] != "killed: example.com/fixture/lib.TestAdd" || flaky.RunVerdicts[1] != "survived" {
		t.Fatalf("flaky verdicts = %v", flaky.RunVerdicts)
	}
	if flaky.Killer != "example.com/fixture/lib.TestAdd" || flaky.KillerOutput == "" {
		t.Fatalf("flaky kill evidence missing: %+v", flaky)
	}

	// A hanging mutant's timeout verdict names the option governing the
	// bound.
	hung := strings.Replace(string(orig), "return a + b", "for {\n\t}\n\treturn a + b", 1)
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(hung), "example.com/fixture/lib", "^TestAdd$", 2*time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "(timeout)" || !strings.Contains(res.KillerOutput, "oracle_timeout_sec") {
		t.Fatalf("hanging mutant = %+v, want a timeout kill naming oracle_timeout_sec", res)
	}

	// A baseline probe exceeding the oracle timeout refuses naming the
	// governing option.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Nanosecond, 1); err == nil || !strings.Contains(err.Error(), "oracle_timeout_sec") {
		t.Fatalf("probe timeout does not name its governing option: %v", err)
	}

	// runs is bounded: each run is a full oracle process.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Minute, 11); err == nil || !strings.Contains(err.Error(), "runs must be between") {
		t.Fatalf("unbounded runs accepted: %v", err)
	}
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Minute, -1); err == nil || !strings.Contains(err.Error(), "runs must be between") {
		t.Fatalf("negative runs accepted: %v", err)
	}
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestFrozenEnvironment$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestFrozenEnvironment" {
		t.Fatalf("frozen-environment mutant = %+v, want attributed kill", res)
	}

	// Breaking only Weak's untested branch: TestWeak cannot see it.
	unseen := strings.Replace(string(orig), "return x - 1", "return x - 2", 1)
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(unseen), "example.com/fixture/lib", "^TestWeak$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("unseen mutant = %+v, want survivor", res)
	}

	// Refusals: identical content, a pattern matching nothing, a test
	// failing on the clean tree.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", orig, "example.com/fixture/lib", "^TestAdd$", time.Minute, 1); err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("identical replacement scored: %v", err)
	}
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestNoSuch$", time.Minute, 1); err == nil || !strings.Contains(err.Error(), "matched no tests") {
		t.Fatalf("zero-match probe scored: %v", err)
	}
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/failing", "^TestAlwaysFails$", time.Minute, 1); err == nil || !strings.Contains(err.Error(), "does not pass on the unmutated tree") {
		t.Fatalf("failing-clean probe scored: %v", err)
	}

	// A replacement that does not compile measured nothing: an error, never
	// a survivor — and the refusal carries the compiler's own diagnostic so
	// the caller repairs the edit from the compiler's reason, not a guess.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte("package lib\nfunc Broken( {"), "example.com/fixture/lib", "^TestAdd$", time.Minute, 1); err == nil || !strings.Contains(err.Error(), "did not compile") {
		t.Fatalf("uncompilable replacement scored: %v", err)
	} else if !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("compile refusal lacks the compiler diagnostic: %v", err)
	}

	// The edits form measures identically to the whole replacement
	// (REQ-exec-ephemeral): state the change, not the file.
	res, err = tr.EphemeralEdits(ctx, "lib/lib.go", []Edit{{Old: "return a + b", New: "return a + b + 1"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("edits mutant = %+v, want killed by TestAdd", res)
	}
	if _, err := tr.EphemeralEdits(ctx, "lib/lib.go", []Edit{{Old: "no such text", New: "x"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute, 1); err == nil {
		t.Fatal("zero-match edit scored")
	}
	res, err = tr.EphemeralBatch(ctx, []BatchEdit{
		{File: "lib/lib.go", OldString: "return a + b", NewString: "return a + b + manualDelta()"},
		{File: "lib/doc.go", OldString: "package lib", NewString: "package lib\n\nfunc manualDelta() int { return 1 }"},
	}, "example.com/fixture/lib", "^TestAdd$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || len(res.Files) != 2 || res.Files[0] != "lib/doc.go" || res.Files[1] != "lib/lib.go" {
		t.Fatalf("multi-file edit batch = %+v", res)
	}

	// The tree was never touched.
	after, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(orig) {
		t.Fatal("the working tree was modified")
	}
	afterDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDoc) != string(origDoc) {
		t.Fatal("the secondary overlaid file was modified")
	}
}

func TestEphemeralRejectsEscapingFiles(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "module")
	if err := os.CopyFS(root, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"../outside.go", outside, `C:\outside.go`, "link.go"} {
		if _, err := tree.Ephemeral(context.Background(), file, []byte("package changed\n"), "example.com/fixture/lib", "^TestAdd$", time.Minute, 1); err == nil || (!strings.Contains(err.Error(), "tree-relative") && !strings.Contains(err.Error(), "escapes")) {
			t.Fatalf("whole replacement accepted %q: %v", file, err)
		}
		if _, err := tree.EphemeralEdits(context.Background(), file, []Edit{{Old: "package", New: "package"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute, 1); err == nil || (!strings.Contains(err.Error(), "tree-relative") && !strings.Contains(err.Error(), "escapes")) {
			t.Fatalf("sequential edits accepted %q: %v", file, err)
		}
	}
}

// A discarded probe names its repairing reason: an environmental-noise
// discard never reads as a compile failure telling the caller to check
// the replacements.
func TestDiscardErrorSplitsNoiseFromCompileFailure(t *testing.T) {
	files := []string{"lib/lib.go"}
	noise := discardError(files, "unclassifiable mutant-run failure: the baseline probe failed alongside the mutant (environmental noise, not a kill): exit 1: ...")
	if strings.Contains(noise.Error(), "did not compile") || !strings.Contains(noise.Error(), "not a measurement") {
		t.Fatalf("noise discard reads as a compile failure: %v", noise)
	}
	compile := discardError(files, "lib/lib.go:3:1: syntax error")
	if !strings.Contains(compile.Error(), "did not compile") || !strings.Contains(compile.Error(), "syntax error") {
		t.Fatalf("compile discard lost its diagnostic: %v", compile)
	}
	bare := discardError(files, "")
	if !strings.Contains(bare.Error(), "did not compile") {
		t.Fatalf("bare discard = %v", bare)
	}
}

// A survivor verdict over a replacement outside the probed oracle's
// exercised set is labeled, never silent: the out-of-closure file
// lands in UnexercisedFiles, while an in-closure covered survivor
// carries no label (REQ-exec-ephemeral).
func TestEphemeralLabelsUnexercisedReplacement(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tr := fixtureTree(t)
	ctx := context.Background()

	outside, err := os.ReadFile("internal/engine/testdata/fixturemod/diskread/disk.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(outside), "return x - 1", "return x - 2", 1)
	if mutated == string(outside) {
		t.Fatal("fixture edit failed")
	}
	res, err := tr.Ephemeral(ctx, "diskread/disk.go", []byte(mutated), "example.com/fixture/lib", "^TestWeak$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("out-of-closure replacement killed: %+v", res)
	}
	if len(res.UnexercisedFiles) != 1 || res.UnexercisedFiles[0] != "diskread/disk.go" {
		t.Fatalf("unexercised label = %v, want the out-of-closure file named", res.UnexercisedFiles)
	}

	inside, err := os.ReadFile("internal/engine/testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	// The large-x arm: TestWeak never exercises it, so the mutant
	// survives while the FILE is covered - no label.
	inMutated := strings.Replace(string(inside), "return x - 1", "return x - 2", 1)
	if inMutated == string(inside) {
		t.Fatal("fixture edit failed")
	}
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(inMutated), "example.com/fixture/lib", "^TestWeak$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("untested-branch mutant killed: %+v", res)
	}
	if len(res.UnexercisedFiles) != 0 {
		t.Fatalf("covered file labeled unexercised: %v", res.UnexercisedFiles)
	}
}

// A failed coverage probe leaves the unexercised label absent and the
// measurement sound - the label is advisory classification, never a
// gate on the verdict (REQ-exec-ephemeral).
func TestEphemeralProbeFailureLeavesLabelAbsent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	restore := coveredPositions
	coveredPositions = func(context.Context, string, string, string, string, time.Duration, []string, []string, engine.DirectiveCoverageView) (engine.Coverage, error) {
		return engine.Coverage{}, errors.New("probe refused")
	}
	defer func() { coveredPositions = restore }()
	tr := fixtureTree(t)
	outside, err := os.ReadFile("internal/engine/testdata/fixturemod/diskread/disk.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(outside), "return x - 1", "return x - 2", 1)
	res, err := tr.Ephemeral(context.Background(), "diskread/disk.go", []byte(mutated), "example.com/fixture/lib", "^TestWeak$", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("out-of-closure replacement killed: %+v", res)
	}
	if res.UnexercisedFiles != nil {
		t.Fatalf("failed probe still labeled: %v", res.UnexercisedFiles)
	}
}
