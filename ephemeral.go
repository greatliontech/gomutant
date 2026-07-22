package gomutant

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/engine"
)

// EphemeralResult is one manual mutant's evidence (REQ-exec-ephemeral): what
// was mutated, the test it ran against, whether that test killed it, and the
// attributed killer. It is evidence for the caller to act on, never
// persisted to a finding record.
type EphemeralResult struct {
	Files   []string `json:"files"`
	TestPkg string   `json:"testPkg"`
	Run     string   `json:"run"`
	Killed  bool     `json:"killed"`
	// Killer names the failing test, a timeout, or a package-scope failure
	// when Killed; empty when the mutant survived.
	Killer string `json:"killer,omitempty"`
}

// Ephemeral runs one manual mutant — a caller-supplied replacement of one
// source file, for the mutations the operator set cannot generate
// (generated-data drift, resolver seams, caller mappings): it overlays file
// with mutant (the whole replacement source), runs the named test (testPkg
// filtered to run), and reports whether the test killed it — all through a
// build overlay, the tree never touched (REQ-exec-ephemeral). Before running
// it probes the named test on the unmutated tree: a run pattern matching
// nothing, or a test already failing clean, cannot attribute a mutant, so
// either refuses the run rather than scoring it. file is tree-relative;
// testPkg is a go package path; run is a -run pattern. A mutant that fails
// to compile is an error, not a survivor: nothing was measured.
func (t *Tree) Ephemeral(ctx context.Context, file string, mutant []byte, testPkg, run string, oracleTimeout time.Duration) (*EphemeralResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if oracleTimeout <= 0 {
		oracleTimeout = 60 * time.Second
	}
	abs, err := resolveTreeFile(t.dir, file)
	if err != nil {
		return nil, err
	}
	// The overlay silently no-ops if abs is not a real source file, and an
	// identical replacement measures nothing — both would read as a false
	// survivor. Resolve and compare against the original first.
	orig, err := readFileContext(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bytes.Equal(orig, mutant) {
		return nil, fmt.Errorf("mutant is identical to %s: nothing to measure", file)
	}

	return t.runEphemeral(ctx, []fileReplacement{{File: file, Abs: abs, Source: mutant}}, testPkg, run, oracleTimeout)
}

func (t *Tree) runEphemeral(ctx context.Context, replacements []fileReplacement, testPkg, run string, oracleTimeout time.Duration) (*EphemeralResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(replacements) == 0 {
		return nil, fmt.Errorf("manual mutant has no file replacements")
	}
	seen := map[string]bool{}
	for i, replacement := range replacements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if replacement.File == "" || replacement.Abs == "" || replacement.Source == nil {
			return nil, fmt.Errorf("manual mutant replacement %d is incomplete", i+1)
		}
		if seen[replacement.Abs] {
			return nil, fmt.Errorf("manual mutant replaces %s more than once", replacement.File)
		}
		seen[replacement.Abs] = true
	}
	if oracleTimeout <= 0 {
		oracleTimeout = 60 * time.Second
	}
	// A rapid property failing on the baseline or against the mutant must
	// never write a reproducer into the tree (REQ-mut-overlay).
	var binFlags []string
	rapid, _, err := t.eng.SplitRapidPkgsContext(ctx, []string{testPkg})
	if err != nil {
		return nil, err
	}
	if len(rapid) > 0 {
		binFlags = []string{"-rapid.nofailfile"}
	}

	env := t.eng.GoEnv()
	ran, passed, err := engine.TestProbeEnv(ctx, t.dir, testPkg, run, oracleTimeout, binFlags, env)
	if err != nil {
		return nil, err
	}
	if ran == 0 {
		return nil, fmt.Errorf("%q matched no tests in %s: nothing can attribute the mutant", run, testPkg)
	}
	if !passed {
		return nil, fmt.Errorf("the named test does not pass on the unmutated tree in %s: a kill against it would be fabricated", testPkg)
	}

	files := make([]string, len(replacements))
	engineReplacements := make([]engine.Replacement, len(replacements))
	for i, replacement := range replacements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files[i] = replacement.File
		engineReplacements[i] = engine.Replacement{File: replacement.Abs, Source: replacement.Source}
	}
	m := engine.Mutant{Replacements: engineReplacements}
	outcome, killer, diagnostic, err := engine.RunMutantEnv(ctx, t.dir, m, []string{testPkg}, run, oracleTimeout, binFlags, env)
	if err != nil {
		return nil, err
	}
	if outcome == engine.MutantDiscarded {
		if diagnostic != "" {
			return nil, fmt.Errorf("mutant did not compile: nothing was measured — check the replacements for %s\n%s", strings.Join(files, ", "), diagnostic)
		}
		return nil, fmt.Errorf("mutant did not compile: nothing was measured — check the replacements for %s", strings.Join(files, ", "))
	}
	return &EphemeralResult{
		Files:   files,
		TestPkg: testPkg,
		Run:     run,
		Killed:  outcome == engine.MutantKilled,
		Killer:  killer,
	}, nil
}

// EphemeralBatch runs one atomic multi-file exact-match edit batch as a manual
// mutant. Every edit resolves against the original files before one overlay
// exposes all effective replacements to the named test.
func (t *Tree) EphemeralBatch(ctx context.Context, edits []BatchEdit, testPkg, run string, oracleTimeout time.Duration) (*EphemeralResult, error) {
	replacements, err := prepareEditBatchContext(ctx, t.dir, edits)
	if err != nil {
		return nil, err
	}
	return t.runEphemeral(ctx, replacements, testPkg, run, oracleTimeout)
}

// Edit is one exact-match replacement inside an ephemeral mutant's source:
// Old must occur exactly once in the current content — a match of zero or
// more than one is refused rather than guessed, because a mutation applied
// somewhere the caller did not mean measures the wrong mutant
// (REQ-exec-ephemeral).
type Edit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ApplyEdits applies exact-match edits to src in order and returns the
// mutated content — the edits form of an ephemeral mutant's replacement
// source (REQ-exec-ephemeral).
func ApplyEdits(src []byte, edits []Edit) ([]byte, error) {
	return ApplyEditsContext(context.Background(), src, edits)
}

// ApplyEditsContext is ApplyEdits with cooperative cancellation.
func ApplyEditsContext(ctx context.Context, src []byte, edits []Edit) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("gomutant: no edits given")
	}
	out := string(src)
	for i, e := range edits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Old == "" {
			return nil, fmt.Errorf("gomutant: edit %d has an empty match", i+1)
		}
		switch n := strings.Count(out, e.Old); n {
		case 0:
			return nil, fmt.Errorf("gomutant: edit %d matches nothing: %q", i+1, e.Old)
		case 1:
			out = strings.Replace(out, e.Old, e.New, 1)
		default:
			return nil, fmt.Errorf("gomutant: edit %d is ambiguous (%d matches): %q", i+1, n, e.Old)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// EphemeralEdits runs an ephemeral mutant given as exact-match edits against
// the file's current content (REQ-exec-ephemeral): the edits are applied and
// the result runs exactly as a whole replacement would.
func (t *Tree) EphemeralEdits(ctx context.Context, file string, edits []Edit, testPkg, run string, oracleTimeout time.Duration) (*EphemeralResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := resolveTreeFile(t.dir, file)
	if err != nil {
		return nil, err
	}
	orig, err := readFileContext(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mutant, err := ApplyEditsContext(ctx, orig, edits)
	if err != nil {
		return nil, err
	}
	return t.Ephemeral(ctx, file, mutant, testPkg, run, oracleTimeout)
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	return contextio.ReadFile(ctx, path)
}
