package gomutant

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// EphemeralResult is one manual mutant's evidence (REQ-exec-ephemeral): what
// was mutated, the test it ran against, whether that test killed it, and the
// attributed killer. It is evidence for the caller to act on, never
// persisted to a finding record.
type EphemeralResult struct {
	File    string `json:"file"`
	TestPkg string `json:"testPkg"`
	Run     string `json:"run"`
	Killed  bool   `json:"killed"`
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
func (t *Tree) Ephemeral(ctx context.Context, file string, mutant []byte, testPkg, run string, timeout time.Duration) (*EphemeralResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	abs, err := filepath.Abs(filepath.Join(t.dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	// The overlay silently no-ops if abs is not a real source file, and an
	// identical replacement measures nothing — both would read as a false
	// survivor. Resolve and compare against the original first.
	orig, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if bytes.Equal(orig, mutant) {
		return nil, fmt.Errorf("mutant is identical to %s: nothing to measure", file)
	}

	// A rapid property failing on the baseline or against the mutant must
	// never write a reproducer into the tree (REQ-mut-overlay).
	var binFlags []string
	if rapid, _ := t.eng.SplitRapidPkgs([]string{testPkg}); len(rapid) > 0 {
		binFlags = []string{"-rapid.nofailfile"}
	}

	ran, passed, err := engine.TestProbe(ctx, t.dir, testPkg, run, timeout, binFlags)
	if err != nil {
		return nil, err
	}
	if ran == 0 {
		return nil, fmt.Errorf("%q matched no tests in %s: nothing can attribute the mutant", run, testPkg)
	}
	if !passed {
		return nil, fmt.Errorf("the named test does not pass on the unmutated tree in %s: a kill against it would be fabricated", testPkg)
	}

	m := engine.Mutant{File: abs, Source: mutant}
	outcome, killer, err := engine.RunMutant(ctx, t.dir, m, []string{testPkg}, run, timeout, binFlags)
	if err != nil {
		return nil, err
	}
	if outcome == engine.MutantDiscarded {
		return nil, fmt.Errorf("mutant did not compile: nothing was measured — check the replacement source for %s", file)
	}
	return &EphemeralResult{
		File:    file,
		TestPkg: testPkg,
		Run:     run,
		Killed:  outcome == engine.MutantKilled,
		Killer:  killer,
	}, nil
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
	if len(edits) == 0 {
		return nil, fmt.Errorf("gomutant: no edits given")
	}
	out := string(src)
	for i, e := range edits {
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
	return []byte(out), nil
}

// EphemeralEdits runs an ephemeral mutant given as exact-match edits against
// the file's current content (REQ-exec-ephemeral): the edits are applied and
// the result runs exactly as a whole replacement would.
func (t *Tree) EphemeralEdits(ctx context.Context, file string, edits []Edit, testPkg, run string, timeout time.Duration) (*EphemeralResult, error) {
	abs, err := filepath.Abs(filepath.Join(t.dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	orig, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	mutant, err := ApplyEdits(orig, edits)
	if err != nil {
		return nil, err
	}
	return t.Ephemeral(ctx, file, mutant, testPkg, run, timeout)
}
