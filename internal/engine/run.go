package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MutantOutcome classifies one overlay run.
type MutantOutcome int

const (
	// MutantDiscarded: the mutant does not compile (or its run was
	// cancelled); it proves nothing — deliberately the zero value, so an
	// unwritten outcome can never read as a verdict.
	MutantDiscarded MutantOutcome = iota
	// MutantKilled: an oracle test failed (or the run timed out — behavior
	// changed).
	MutantKilled
	// MutantSurvived: every oracle test passed against the mutant.
	MutantSurvived
)

// rapidPkg is the recognized property-test library: its check runners
// persist a failure reproducer into the tree unless told not to, which a
// mutant run must never allow (REQ-mut-overlay).
const rapidPkg = "pgregory.net/rapid"

// SplitRapidPkgs partitions test packages by whether their test files
// (in-package or external variant) import pgregory.net/rapid. Rapid packages
// need -rapid.nofailfile so a mutant-induced property failure never writes a
// reproducer into the source tree — and one mutant's failfile cannot replay
// into the next mutant's run (REQ-mut-overlay). The flag is per-binary: a
// test binary that does not register it fails on the unknown flag and reads
// as a false kill, so the two groups must run in separate invocations. The
// scan is of direct imports only — a test driving rapid solely through a
// helper package escapes the guard; the failure mode there is visible
// failfile litter, never a false kill.
func (t *Tree) SplitRapidPkgs(testPkgs []string) (rapid, plain []string) {
	byPath := map[string]bool{}
	for _, pkg := range t.pkgs {
		if byPath[pkg.PkgPath] {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == rapidPkg {
					byPath[pkg.PkgPath] = true
				}
			}
		}
	}
	for _, p := range testPkgs {
		if byPath[p] || byPath[p+"_test"] {
			rapid = append(rapid, p)
		} else {
			plain = append(plain, p)
		}
	}
	return rapid, plain
}

// TimeoutKiller is the killer attribution of a timed-out mutant run: the
// hang itself is the noticed breakage, so no named test claims the kill
// (REQ-exec-attribution).
const TimeoutKiller = "(timeout)"

// PackageKillerPrefix prefixes the killer attribution of a mutant that
// breaks a test binary at package scope — a panic in a goroutine, an
// os.Exit, a TestMain failure — where go test emits no test-level fail
// event. Such a kill is admitted only after a differential baseline probe
// clears the environment (REQ-exec-attribution).
const PackageKillerPrefix = "(package failure: "

// RunMutant executes the oracle tests against one mutant through a build
// overlay — the tree is never touched (REQ-mut-overlay). binFlags are passed
// to the test binaries after the package list.
//
// testPkgs must all be scoped by runRegex as their own oracle pattern: an
// oracle spanning differently-named tests runs per package
// (REQ-exec-oracle-run) — one union pattern would also run a same-named
// non-oracle test in a sibling package, whose failure is unattributable and
// aborts the sweep.
//
// A kill must be attributed (REQ-core-attributed-kills): a named failing
// test in the run's -json stream (returned as "<pkg>.<TopLevelTest>"), a
// timeout (TimeoutKiller — behavior changed: it hangs), or a package-scope
// failure the baseline probe attributes to the mutant (PackageKillerPrefix).
// A run that fails any other way is environmental noise — an unregistered
// flag, a loaded machine, a dying binary — and returns an error, never a
// kill: a corrupted measurement must never read as a sound one.
func RunMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string) (MutantOutcome, string, error) {
	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", err
	}
	defer os.RemoveAll(tmp)
	mutFile := filepath.Join(tmp, "mutant.go")
	if err := os.WriteFile(mutFile, m.Source, 0o644); err != nil {
		return MutantDiscarded, "", err
	}
	overlay := filepath.Join(tmp, "overlay.json")
	oj := fmt.Sprintf(`{"Replace": {%q: %q}}`, m.File, mutFile)
	if err := os.WriteFile(overlay, []byte(oj), 0o644); err != nil {
		return MutantDiscarded, "", err
	}

	parent := ctx
	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// -failfast: one oracle failure decides the binary's verdict; the
	// remaining tests in it prove nothing further about this mutant.
	baseArgs := append([]string{"test", "-json", "-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	baseArgs = append(baseArgs, binFlags...)
	args := append([]string{"test", "-json", "-overlay", overlay, "-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	args = append(args, binFlags...)
	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Dir = dir
	cmd.Env = goworkEnv(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	killer := firstFailingTest(stdout.Bytes())
	switch {
	case runErr == nil:
		return MutantSurvived, "", nil
	case runCtx.Err() == context.DeadlineExceeded:
		return MutantKilled, TimeoutKiller, nil
	case runCtx.Err() != nil:
		// A cancelled run proves nothing about the mutant.
		return MutantDiscarded, "", ctx.Err()
	case strings.Contains(stdout.String(), "[build failed]"):
		return MutantDiscarded, "", nil
	case killer != "":
		return MutantKilled, killer, nil
	}

	// The run failed with no test-level attribution. Two very different
	// causes share this shape: the mutant breaking the binary at package
	// scope (a goroutine panic, an os.Exit, a TestMain failure — the
	// strongest kind of kill), and environmental noise. A differential
	// baseline probe — the same invocation without the overlay — tells them
	// apart: noise fails the baseline too; a mutant-caused break does not
	// (REQ-exec-attribution).
	if pkg := failedPackage(stdout.Bytes()); pkg != "" {
		baseCtx, baseCancel := context.WithTimeout(parent, timeout)
		defer baseCancel()
		base := exec.CommandContext(baseCtx, "go", baseArgs...)
		base.Dir = dir
		base.Env = goworkEnv(dir)
		baseErr := base.Run()
		if baseCtx.Err() != nil {
			// A cancelled probe proves nothing — never "noise".
			return MutantDiscarded, "", baseCtx.Err()
		}
		if baseErr == nil {
			return MutantKilled, PackageKillerPrefix + pkg + ")", nil
		}
	}
	return MutantDiscarded, "", fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
}

// TestProbe runs the named test on the unmutated tree and reports how many
// top-level tests ran and whether the run passed. It is the baseline an
// ephemeral run needs before scoring anything (REQ-exec-ephemeral): a -run
// matching zero tests, or a test already failing on the clean tree, cannot
// attribute a mutant, so a verdict against it would be a fabricated finding.
func TestProbe(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string) (ran int, passed bool, err error) {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// binFlags carries -rapid.nofailfile for rapid packages: a property that
	// fails on the clean baseline would otherwise write a reproducer into
	// the tree, the very invariant the runner protects (REQ-mut-overlay).
	args := append([]string{"test", "-json", "-count=1", "-run", run, testPkg}, binFlags...)
	cmd := exec.CommandContext(ctx2, "go", args...)
	cmd.Dir = dir
	cmd.Env = goworkEnv(dir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	if ctx2.Err() == context.DeadlineExceeded {
		return 0, false, fmt.Errorf("baseline test timed out")
	}
	if strings.Contains(buf.String(), "[build failed]") {
		return 0, false, fmt.Errorf("baseline test failed to build")
	}
	return countTopTests(buf.Bytes()), runErr == nil, nil
}

// failedPackage scans a go test -json stream for a package-level fail event,
// returning the package or empty.
func failedPackage(stream []byte) string {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return ""
		}
		if e.Action == "fail" && e.Test == "" && e.Package != "" {
			return e.Package
		}
	}
	return ""
}

// firstFailingTest scans a go test -json stream for the first test-level
// fail event, returning the failing test as "<pkg>.<TopLevelTest>" — the
// symbol form oracles pin. The subtest path is stripped HERE, where the Test
// field is unambiguous; in the joined form the first "/" lands inside the
// import path.
func firstFailingTest(stream []byte) string {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return ""
		}
		if e.Action == "fail" && e.Test != "" {
			name := e.Test
			if i := strings.Index(name, "/"); i >= 0 {
				name = name[:i]
			}
			return e.Package + "." + name
		}
	}
	return ""
}

// countTopTests counts the distinct top-level tests (excluding subtests)
// that reported a pass or fail in a go test -json stream.
func countTopTests(stream []byte) int {
	type event struct{ Action, Test string }
	seen := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			break
		}
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		if e.Action == "pass" || e.Action == "fail" {
			seen[e.Test] = true
		}
	}
	return len(seen)
}

// tail returns the last n bytes of s, for error surfacing.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
