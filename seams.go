package gomutant

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// runSeams are the package's test injection points — every engine call on
// a verdict-bearing or measured path, and every observer or knob a test
// installs — in one struct, read through the one variable seams.
// Production never writes a field: each stays at its default (the engine's
// own function, a nil observer, a zero knob), so a seam is never a
// behaviour switch. The fields are read unsynchronized from the run's
// goroutines, so the package's tests run sequentially — a parallel test
// swapping one mid-Run would race the read and leak into its siblings.
type runSeams struct {
	// coveredPositions is the baseline coverage probe, the schedule's
	// and the survivor buckets' as much as the ephemeral probe's: a
	// seam so tests supply crafted coverage without constructing real
	// coverage fixtures per shape, and so the probe-failure arm — the
	// label stays absent and the measurement stays sound — is testable
	// without a genuinely unbuildable probe.
	coveredPositions func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, binFlags, env []string, view engine.DirectiveCoverageView, bounds engine.OracleBounds) (engine.Coverage, error)
	// testProbe is the plain baseline probe — the ephemeral baseline,
	// and the shaped path's clean-twin probe that decides whether a
	// compile refusal is the oracle's kill or a scratch fault: a seam
	// so the bound each mode hands it — the measurement leash in
	// derived-budget mode, the caller's override otherwise — is
	// pinnable by a delegating recorder without constructing a
	// genuinely slow baseline, and so no verdict is judged for real
	// behind a stub's back.
	testProbe func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (int, bool, string, error)
	// runMutantEvidence is the ephemeral mutant runner: tests plant a
	// compiler crash the probe must retry once and never read as a
	// verdict.
	runMutantEvidence func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, string, string, error)
	// runMutantShaped is the shaped path's mutant runner — the
	// scratch-twin execution under the group's budget — the
	// verdict-bearing process a stubbed run must not pay for real.
	runMutantShaped func(ctx context.Context, dir, cleanDir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env, cleanEnv []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, string, error)
	// runMutantObserved is the group executor's engine call: a seam so
	// a test can observe the executed patterns — the exemption's one
	// direct observable is which patterns ran (the exempt remainder's
	// never appears outside the audit's full runs).
	runMutantObserved func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error)
	// baselineProbe is the baseline a group pays once: its preparation
	// baseline, served from the bank when the tree is unchanged, under
	// the leash the bank lifts and the producer-probe gate held shared
	// — one seam, so a test counting or bounding the banked baselines
	// counts every one and neither a kill's (killGround) nor an
	// advisory's (advisoryProbe).
	baselineProbe func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error)
	// advisoryProbe is the solo-test probe of oracle guidance's
	// instability attribution — one per oracle test under the group's
	// advisory leash, ungated and never banked, best-effort: a stub sees
	// every probe an attribution pays and a counter of banked baselines
	// never counts one.
	advisoryProbe func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error)
	// killGround is the baseline a kill is judged against — the
	// phase-pattern vouch on a scheduled kill, and the killer-scoped
	// differential ground a serial confirmation is scored against — a
	// narrowed group's pass on the unmutated tree, paid per kill at the
	// run's oracle timeout and never banked: its own seam, so a test
	// stubbing the group baselines leaves no kill judged for real
	// behind its back, and one counting banked baselines never counts
	// a kill's.
	killGround func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error)
	// probeGateInstalled observes the run's producer-probe gate at its
	// installation: a test holds the gate's handle and pins, from
	// inside a confirmation, that the confirmation runs under the gate
	// held exclusively (REQ-exec-attribution's isolation from
	// preparation probes).
	probeGateInstalled func(gate *sync.RWMutex)
	// subjectViewBuild observes each subject-view build with its
	// requested symbols — the one-build claims (the batched judge's one
	// set per posture, the campaign's one build per mode shared by the
	// decision and the producer roles), fired from the one build loop
	// over resolved groups so every build is counted whatever its
	// caller's fault disposition; a strict call aborting at resolution
	// built nothing and fires nothing.
	subjectViewBuild func(symbols []string)
	// observedUnion observes each proof capture pass with the symbols
	// it covers, fired before the first module's capture — the
	// capture-time fault routes (a tree moving between a strict build's
	// construction and its proof capture).
	observedUnion func(symbols []string)
	// validateProducers stands in for a view set's producer validation
	// — a test hands the run the engine's own analysis-unavailable
	// verdict, which no fixture reaches deterministically once the
	// capture's proofs persist for the validation to serve, and pins
	// the unverifiable stamp against the refusal every other verdict
	// earns.
	validateProducers func(ctx context.Context, views *subjectViewSet) error
	// inspectionSupplementaryView observes the supplementary view build
	// for symbols a caller-supplied prebuilt set does not cover — the
	// event that proves the run's stale-reason enrichment reuses the
	// run's own views.
	inspectionSupplementaryView func(symbols []string)
	// windowCandidates fixes the execution window when positive — BOTH
	// bounds, the ceiling and the minimum, so the execution budget never
	// splits a fixed window; zero means the jobs-derived rule
	// (windowcost.Bounds).
	windowCandidates int
	// waitPreparedBeforePick holds each window pick until preparation
	// completes: value-order assertions see the whole ready pool instead
	// of racing the serial preparation. Production picks never wait: a
	// window that has not been gathered is not ready.
	waitPreparedBeforePick bool
	// truncateAfterItems, when positive, makes the preparation pipeline
	// stop delivering work after that many items with the stopping
	// error lost — the truncation shape REQ-exec-completion refuses.
	truncateAfterItems int
	// truncateErr, when non-nil beside truncateAfterItems, is surfaced
	// as the preparation pipeline's own error instead of being
	// swallowed: the error path's held-window aggregation — completed
	// windows, the held one included, commit before the error returns
	// (REQ-exec-attribution's abort terms).
	truncateErr error
	// ephemeralBudgetFloor keeps a derived mutant budget from ever being
	// less patient than the fixed default it replaced: the floor is the
	// retired 60s, because a warm-cache baseline pays no compile while
	// the mutant run always recompiles the mutated package inside its
	// bound — the multiple alone would time out honest slow mutants of
	// fast tests, and a timeout is a kill, the flattering direction. A
	// seam only so tests can pin the measured derivation and the
	// timeout-kill serve without minute-class hangs.
	ephemeralBudgetFloor time.Duration
}

// seams is the one variable, written by tests and read by the run;
// defaultSeams builds the value production runs on.
var seams = defaultSeams()

func defaultSeams() runSeams {
	return runSeams{
		coveredPositions:     engine.CoveredPositions,
		testProbe:            engine.TestProbeEnv,
		runMutantEvidence:    engine.RunMutantEvidenceEnv,
		runMutantObserved:    engine.RunMutantObservedEnv,
		runMutantShaped:      engine.RunMutantBaselineDirEnv,
		advisoryProbe:        engine.TestProbeObservedEnv,
		baselineProbe:        engine.TestProbeObservedEnv,
		killGround:           engine.TestProbeObservedEnv,
		ephemeralBudgetFloor: 60 * time.Second,
	}
}

// errTruncateSeam is the stopping error truncateAfterItems loses.
var errTruncateSeam = errors.New("truncation seam")
