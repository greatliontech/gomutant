package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

var findingObservationSequence atomic.Uint64

// Options bound a run.
type Options struct {
	// Budget caps selected candidates per symbol; 0 means all (REQ-mut-budget).
	Budget int
	// OracleTimeout bounds each oracle process; 0 means 60s.
	OracleTimeout time.Duration
	// Force re-measures targets whose prior finding's pins still match.
	Force bool
	// Guidance receives oracle-instability attribution for a measured
	// target whose merged runtime evidence landed unverifiable under a
	// package-derived oracle: each oracle test probed individually, the
	// unstable ones named with a narrowing suggestion
	// (REQ-exec-oracle-guidance). Nil skips the attribution probes.
	Guidance func(OracleGuidance)
	// BracketPaths declares external surfaces the oracle legitimately
	// reads — module-relative paths (a file or a directory tree) or
	// absolute files — extending each spawn's observation bracket beyond
	// the oracle package directory (REQ-exec-observation). An absolute
	// external directory cannot be walked and is refused at run start,
	// as is a path under a tool-excluded directory. Declaring a path
	// carries the bracket contract's mutation-free assertion for the
	// span.
	BracketPaths []string
	// Jobs bounds concurrent mutant runs; 0 means half the CPUs. Mutant runs
	// are process-isolated (own overlay, own temp dir, shared
	// content-addressed build cache), so they parallelize safely — but
	// load-induced flakes read as kills, so the default hedges.
	Jobs int
	// Prior findings (a parsed document): a target whose pins all hold is
	// served from here instead of re-measured (REQ-result-stale).
	Prior []Finding
	// Decision receives each target's deterministic pre-execution disposition
	// in target order (REQ-exec-run-status).
	Decision func(RunDecision)
	// PlanOnly stops the run after the deterministic preparation sequence
	// and target decisions: mutants are enumerated and every decision is
	// computed and delivered, but no baseline probes, no mutant executes,
	// and nothing new is persisted (cached serves already committed are
	// idempotent re-merges of existing records). The return carries only
	// the findings complete without execution — cached serves and skips —
	// so the decisions are the plan and precondition holes surface before
	// any budget is spent (REQ-exec-run-status's plan clause).
	PlanOnly bool
	// PlanOnly also suppresses Commit at the library boundary — the
	// no-write guarantee is the run's, not each caller's to re-implement.
	// Executing must be safe for synchronous invocation from the run loop
	// and receives advisory execution-phase progress: which target window
	// is dispatching or confirming, exact campaign-wide candidate
	// tallies, and per-kill confirmation progress while confirming.
	// Events are diagnostic, carry no ordering or completion
	// guarantee beyond target-window boundaries, and never enter a
	// decision or finding (REQ-exec-run-status's advisory classes). The
	// callback must return normally.
	Executing func(ExecutionEvent)
	// Progress synchronously receives deterministic preparation events before
	// terminal target decisions and mutant execution. It must return normally
	// (REQ-exec-run-status).
	Progress func(PreparationEvent)
	// AnalysisProgress must be safe for concurrent invocation and synchronously receives advisory keep-alive events from
	// the run's freshness analysis — the gofresh phase name and, for
	// per-package phases, the package. Events are diagnostic, carry no
	// completion signal, never enter a decision or finding, and their sequence
	// is not part of the deterministic run-status contract
	// (REQ-exec-run-status). The callback must return normally.
	AnalysisProgress func(phase, pkg string)
	// Commit synchronously receives each finished target's final finding —
	// a cached serve as soon as its pins are proven to hold, a measured or
	// spliced target after its post-execution producer validation — so the
	// caller can persist completed targets incrementally and an interrupted
	// run keeps every finding committed before cancellation became observable
	// (REQ-exec-cancellation). The caller's final merge of the returned
	// findings remains the authority; re-merging a committed finding is
	// idempotent. A returned error aborts the run. Skipped targets measure
	// nothing and are never delivered.
	Commit         func(Finding) error
	afterExecution func()
	aggregate      func()
	producer       func(string)
	// proofAttempt observes each freshness-proof construction attempt —
	// ("", 1) before the shared union pass, then (symbol, 2) before a
	// faulted target's bounded per-target retry — a test seam pinning
	// the union/retry split, like producer above.
	proofAttempt func(symbol string, attempt int)
	// Contradiction receives each attested survivor a growth serve's added
	// tests killed: the attestation is shed — evidence beats attestation —
	// and the contradiction is worth a human's attention, because a mutant
	// judged equivalent was just distinguished (REQ-attest-survivor,
	// REQ-result-stale's growth carve-out). Nil drops the reports; the
	// shed itself is unconditional.
	Contradiction func(AttestationContradiction)
	// dispatched observes each candidate index handed to the worker pool —
	// a test seam pinning suffix-only dispatch under a budget extension,
	// flagged-only dispatch under a candidate-evidence serve, and
	// survivor-only dispatch under a growth serve, like producer above.
	dispatched func(symbol string, mi int)
}

// PreparationStage identifies one observable pre-execution operation.
type PreparationStage string

const (
	PreparationLoading   PreparationStage = "loading"
	PreparationResolving PreparationStage = "resolving"
	PreparationFreshness PreparationStage = "freshness"
	PreparationMutants   PreparationStage = "mutants"
	PreparationBaseline  PreparationStage = "baseline"
)

// ExecutionEvent is one advisory execution-phase progress report: the
// window's phase (executing or confirming), the 1-based index and count
// of targets whose candidates the campaign has dispatched, exact
// campaign-wide candidate tallies (carried and non-runnable candidates
// included — the plan's own counting), and, while confirming, the
// window's serial confirmation progress. Advisory only —
// timing-dependent, outside the deterministic run-status sequence.
type ExecutionEvent struct {
	Phase           string
	TargetIndex     int
	TargetCount     int
	Symbol          string
	CandidatesDone  int
	CandidatesTotal int
	// ConfirmationsDone/Total report the window's serial kill
	// confirmation progress; zero totals outside confirming phases.
	// Total is the upper bound (every confirmable kill) — the stride
	// gate finishes below it in every clean window.
	ConfirmationsDone  int
	ConfirmationsTotal int
}

// PreparationEvent reports one operation before it begins. Symbol is set for
// target-scoped operations; Package is additionally set for baseline probes.
type PreparationEvent struct {
	Stage   PreparationStage `json:"stage"`
	Symbol  string           `json:"symbol,omitempty"`
	Package string           `json:"package,omitempty"`
}

// RunDecision explains whether one target is cached, skipped, or measured.
type RunDecision struct {
	Symbol     string `json:"symbol"`
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
}

// RunSummary is the aggregate final disposition of one selected target set.
type RunSummary struct {
	Targets   int `json:"targets"`
	Measured  int `json:"measured"`
	Cached    int `json:"cached"`
	Skipped   int `json:"skipped"`
	Generated int `json:"generated"`
	Discarded int `json:"discarded"`
	Killed    int `json:"killed"`
	Survived  int `json:"survived"`
	Attested  int `json:"attested"`
	Open      int `json:"open"`
}

// SummarizeRun derives deterministic aggregate totals from findings.
func SummarizeRun(findings []Finding) RunSummary {
	summary := RunSummary{Targets: len(findings)}
	for _, finding := range findings {
		switch {
		case finding.Skipped != "":
			summary.Skipped++
		case finding.Cached:
			summary.Cached++
		default:
			summary.Measured++
		}
		summary.Generated += finding.Generated
		summary.Discarded += finding.Discarded
		summary.Killed += finding.Killed
		summary.Survived += finding.Mutants - finding.Killed
		summary.Attested += len(finding.Attested)
		summary.Open += len(finding.Open())
	}
	return summary
}

// group is one test-binary invocation: a package, its oracle's -run
// pattern, and the binary's flags.
type group struct {
	pkgs                  []string
	runRegex              string
	flags                 []string
	moduleDir, packageDir string
}

type packageContextResult struct {
	moduleDir, packageDir string
	err                   error
}

type oracleValidationResult struct {
	err error
}

type runPreparation struct {
	packageOf      func(context.Context, string) (string, string, error)
	testsOf        func(context.Context, string) ([]string, error)
	validate       func(context.Context, []string) error
	contextFor     func(context.Context, string) (string, string, error)
	splitRapidPkgs func(context.Context, []string) ([]string, []string, error)

	verifyEnumeration func(context.Context, string, []string) error
	derivedOracles    map[string][]string
	validations       map[string]oracleValidationResult
	contexts          map[string]packageContextResult
	rapid             map[string]bool
}

func newRunPreparation(t *Tree) *runPreparation {
	return &runPreparation{
		packageOf:         t.eng.PackageOfContext,
		testsOf:           t.eng.TestsOfContext,
		validate:          t.eng.ValidateOracleContext,
		contextFor:        t.eng.PackageContextContext,
		splitRapidPkgs:    t.eng.SplitRapidPkgsContext,
		verifyEnumeration: t.eng.VerifyTestEnumerationContext,
		derivedOracles:    map[string][]string{},
		validations:       map[string]oracleValidationResult{},
		contexts:          map[string]packageContextResult{},
	}
}

func (p *runPreparation) oracle(ctx context.Context, target Target) ([]string, error) {
	if len(target.Oracle) > 0 || target.OracleExplicit {
		return slices.Clone(target.Oracle), ctx.Err()
	}
	pkg, _, err := p.packageOf(ctx, target.Symbol)
	if err != nil {
		return nil, err
	}
	if pkg == "" {
		return nil, nil
	}
	if oracle, ok := p.derivedOracles[pkg]; ok {
		return slices.Clone(oracle), ctx.Err()
	}
	oracle, err := p.testsOf(ctx, pkg)
	if err != nil {
		return nil, err
	}
	// The derived set is an oracle pin only if it is provably fresh: the
	// package loader's snapshot has been observed lagging the filesystem, so
	// the enumeration is cross-checked against a direct parse of the
	// package's on-disk test files before it is trusted — once per package,
	// memoized with the set it vouches for.
	if p.verifyEnumeration != nil {
		if err := p.verifyEnumeration(ctx, pkg, oracle); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.derivedOracles[pkg] = slices.Clone(oracle)
	return oracle, nil
}

func (p *runPreparation) validateOracle(ctx context.Context, oracle []string) error {
	key := sequenceKey(oracle)
	if result, ok := p.validations[key]; ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return result.err
	}
	err := p.validate(ctx, oracle)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return cancelErr
	}
	p.validations[key] = oracleValidationResult{err: err}
	return err
}

func (p *runPreparation) packageContext(ctx context.Context, pkg string) (string, string, error) {
	if result, ok := p.contexts[pkg]; ok {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		return result.moduleDir, result.packageDir, result.err
	}
	moduleDir, packageDir, err := p.contextFor(ctx, pkg)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return "", "", cancelErr
	}
	p.contexts[pkg] = packageContextResult{moduleDir: moduleDir, packageDir: packageDir, err: err}
	return moduleDir, packageDir, err
}

func (p *runPreparation) rapidPackages(ctx context.Context, candidates []string) (map[string]bool, error) {
	if p.rapid != nil {
		return p.rapid, ctx.Err()
	}
	rapid, _, err := p.splitRapidPkgs(ctx, candidates)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.rapid = map[string]bool{}
	for _, pkg := range rapid {
		p.rapid[pkg] = true
	}
	return p.rapid, nil
}

func sequenceKey(values []string) string {
	var key strings.Builder
	for _, value := range values {
		fmt.Fprintf(&key, "%d:", len(value))
		key.WriteString(value)
	}
	return key.String()
}

// Run mutates each target and executes its oracle per mutant, fanning
// mutant runs across a worker pool (REQ-exec-oracle-run). Prior findings
// are served only when every target and oracle evidence record, operator,
// oracle-timeout and budget pins hold, unless forced (REQ-result-stale). A run that
// cannot attribute an outcome aborts without findings
// (REQ-core-attributed-kills).
// probeOracleInstability attributes unverifiable runtime evidence under
// a package-derived oracle by probing each oracle test alone: a test
// whose solo baseline run produces unverifiable evidence is the
// instability's producer; a clean sweep means no single test reproduces
// it (REQ-exec-oracle-guidance). Probes are best-effort - a probe that
// errors, matches nothing, or fails skips its test rather than
// aborting the run whose finding already committed.
func (t *Tree) probeOracleInstability(ctx context.Context, oracle []string, groups []group, opts Options, runEnv []string) (oracleAttribution, error) {
	byPkg := make(map[string]group, len(groups))
	for _, g := range groups {
		byPkg[g.pkgs[0]] = g
	}
	var attr oracleAttribution
	for _, test := range oracle {
		if err := ctx.Err(); err != nil {
			return oracleAttribution{}, err
		}
		pkg, fn := splitTestSymbol(test)
		g, ok := byPkg[pkg]
		if pkg == "" || fn == "" || !ok {
			continue
		}
		_, passed, observed, err := engine.TestProbeObservedEnv(ctx, t.dir, pkg, "^"+regexp.QuoteMeta(fn)+"$", opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, runEnv)
		if err != nil {
			if ctx.Err() != nil {
				return oracleAttribution{}, ctx.Err()
			}
			if attr.firstErr == "" {
				attr.firstErr = err.Error()
			}
			continue
		}
		attr.completed++
		if passed && observed.Unverifiable {
			attr.unstable = append(attr.unstable, test)
		}
	}
	return attr, nil
}

// AttestationContradiction reports one attested survivor a growth serve's
// added tests killed, with the shed attestation's reasoning and the killer
// so the re-judgment starts from the evidence.
type AttestationContradiction struct {
	Symbol   string
	Position string
	Operator string
	Killer   string
	Reason   string
}

// work is one target's resolved measurement state across the run's
// three phases.
type work struct {
	target      int
	oracle      []string
	reason      string
	candidates  []engine.Candidate
	groups      []group
	oracleSet   map[string]bool
	targetView  *subjectView
	oracleViews []*subjectView
	producer    *subjectViewSet
	// currentLedger is the target package's compartment ledger, read from
	// the view's observation at preparation time: the value is fixed at
	// view build (every sibling narrowing serves the union's one
	// observation), so one read here keeps phase three free of
	// per-finding view reads.
	currentLedger gofresh.TestVariantLedger
	baselines     []runtimeinput.Observation
	// serve is the prior record being served with candidate-local
	// re-execution (REQ-result-stale): only the candidate indexes in
	// flagged execute, and phase three splices their fresh outcomes and
	// evidence into the served record.
	serve   *Finding
	flagged map[int]bool
	// extend is the prior capped record whose measured prefix stands under
	// a wider budget request (REQ-mut-budget, REQ-result-stale's
	// budget-extension carve-out): candidates below extendFrom keep their
	// recorded outcomes, only the unmeasured suffix executes, and phase
	// three splices the suffix outcomes onto the record.
	extend     *Finding
	extendFrom int
	// grow is the prior record served under the oracle-growth carve-out
	// (REQ-result-stale): the derived oracle grew by growAdded while the
	// compartment delta classified inert, so only the recorded survivors
	// (growSurvivors, by candidate index) re-execute — against oracle
	// groups built over the added tests alone — and phase three splices
	// their fresh outcomes into the record under the current tree's
	// evidence.
	grow          *Finding
	growSurvivors map[int]bool
	growAdded     []string
	// drift is the prior record served under the killer-drift carve-out
	// (REQ-result-stale): the target package's compartment moved by an
	// attributable delta, driftMoved names the oracles whose evidence or
	// reference walk observed it, and only the candidate indexes in
	// driftRemeasure — every survivor, every kill whose killer moved, and
	// every timeout or package-scope kill when anything moved — re-execute
	// against the full current oracle; kills keyed to unmoved oracles and
	// all discards keep their recorded dispositions, and phase three
	// splices the fresh outcomes into the record under the current tree's
	// evidence.
	drift          *Finding
	driftMoved     []string
	driftRemeasure map[int]bool
}

// executeMutant runs one mutant through its oracle groups and merges
// the process observations - the shared execution the worker pool and
// the serial kill confirmation both use.
func (t *Tree) executeMutant(ctx context.Context, w work, m engine.Mutant, opts Options, runEnv []string) (engine.MutantOutcome, string, runtimeinput.Observation, string, error) {
	outcome := engine.MutantSurvived
	killer := ""
	incompleteReason := ""
	var processStates []runtimeinput.Observation
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return outcome, killer, runtimeinput.Observation{}, "", err
		}
		if outcome != engine.MutantSurvived {
			break
		}
		out, groupKiller, state, incomplete, diagnostic, err := engine.RunMutantObservedEnv(ctx, t.dir, m, g.pkgs, g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, runEnv)
		if diagnostic != "" {
			// The mutant failed this group's build: no test process
			// started, so the group contributes no runtime observation and
			// no incomplete-process evidence — the discard is a pure
			// function of the mutant source under the toolchain and
			// build-configuration pins the serve validates
			// (REQ-result-stale). Groups that ran keep their observations.
			outcome = out
			killer = groupKiller
			if err != nil {
				return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
			}
			continue
		}
		processStates = append(processStates, state)
		if incompleteReason == "" {
			incompleteReason = incomplete
		}
		if err == nil && out == engine.MutantKilled {
			err = attributedKill(groupKiller, w.oracleSet)
		}
		if err != nil {
			return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
		}
		outcome = out
		killer = groupKiller
	}
	if err := ctx.Err(); err != nil {
		return outcome, killer, runtimeinput.Observation{}, "", err
	}
	state, err := mergeFindingObservationsContext(ctx, t.dir, runEnv, processStates...)
	if err != nil {
		return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
	}
	return outcome, killer, state, incompleteReason, nil
}

// bucketSurvivorExecution classifies why each survivor lived
// (REQ-exec-survivor-evidence): unverifiable runtime evidence buckets
// every survivor "unstable-oracle" without probing; otherwise one
// baseline coverage probe per oracle group (cached across the run's
// targets sharing a group and cover package) decides "never-executed"
// versus "executed-and-passed" by whether any executed block spans the
// mutated position. Advisory classification on the unmutated tree,
// never a measurement pin. Survivors below from keep their recorded
// buckets verbatim: a splice carries its served prefix's advisory data —
// measured under the record's own verifiable conditions — like its
// dispositions and attestations, immune to a suffix-local divergence
// stamp or re-probe.
func (t *Tree) bucketSurvivorExecution(ctx context.Context, f *Finding, w work, opts Options, runEnv []string, cache map[string]engine.Coverage, from int) error {
	if from >= len(f.Survivors) {
		return nil
	}
	if f.TargetEvidence.RuntimeUnverifiable {
		for si := from; si < len(f.Survivors); si++ {
			f.Survivors[si].Execution = "unstable-oracle"
		}
		return nil
	}
	coverage, probed, err := t.oracleCoverage(ctx, w, opts, runEnv, cache)
	if err != nil {
		return err
	}
	if !probed {
		// Best-effort: an unprobeable oracle leaves the bucket empty
		// rather than failing a run whose measurement is already sound.
		return nil
	}
	coverPkg := w.targetView.subject.Package
	for si := from; si < len(f.Survivors); si++ {
		file, line, col, ok := splitSurvivorPosition(f.Survivors[si].Position)
		if !ok {
			continue
		}
		if coverage.Covered(coverPkg+"/"+file, line, col) {
			f.Survivors[si].Execution = "executed-and-passed"
		} else {
			f.Survivors[si].Execution = "never-executed"
		}
	}
	return nil
}

// splitSurvivorPosition splits "file.go:line:col" (an occurrence suffix
// "#n" stripped from the column).
func splitSurvivorPosition(position string) (file string, line, col int, ok bool) {
	parts := strings.Split(position, ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}
	colPart, _, _ := strings.Cut(parts[2], "#")
	line, lineErr := strconv.Atoi(parts[1])
	col, colErr := strconv.Atoi(colPart)
	if lineErr != nil || colErr != nil {
		return "", 0, 0, false
	}
	return parts[0], line, col, true
}

// validateBracketPaths refuses declarations the observation bracket
// cannot honor, loudly and before any measurement: an absolute external
// directory cannot be walked by the bracket's hashing semantics (its
// capture would seal every observation in the run - strictly worse than
// not declaring), and a path under a tool-excluded directory would be
// silently uncovered (REQ-exec-observation).
func validateBracketPaths(paths []string) error {
	for _, p := range paths {
		if filepath.IsAbs(p) {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				return fmt.Errorf("gomutant: bracket path %s is an absolute directory the observation bracket cannot walk; declare it module-relative or declare the files it contains", p)
			}
			continue
		}
		clean := path.Clean(filepath.ToSlash(p))
		for _, excluded := range []string{".git", ".stipulator", ".gomutant"} {
			if clean == excluded || strings.HasPrefix(clean, excluded+"/") {
				return fmt.Errorf("gomutant: bracket path %s lies under tool-excluded %s and would be silently uncovered", p, excluded)
			}
		}
	}
	return nil
}

func (t *Tree) Run(ctx context.Context, targets []Target, opts Options) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Budget < 0 {
		return nil, fmt.Errorf("gomutant: budget must be non-negative")
	}
	if opts.PlanOnly {
		// The plan clause's no-write guarantee is the run's own: even a
		// cached serve's incremental commit is suppressed here, so no
		// caller has to re-implement the suppression.
		opts.Commit = nil
	}
	if opts.OracleTimeout < 0 {
		return nil, fmt.Errorf("gomutant: oracle timeout must be non-negative")
	}
	if err := validateBracketPaths(opts.BracketPaths); err != nil {
		return nil, err
	}
	if opts.OracleTimeout == 0 {
		opts.OracleTimeout = 60 * time.Second
	}
	targets = snapshotTargets(targets)
	opts.Prior = snapshotFindings(opts.Prior)
	repository, err := captureRepositoryStateContext(ctx, t.dir)
	if err != nil {
		return nil, err
	}
	runEnv := t.eng.GoEnv()
	preparation := newRunPreparation(t)
	engines := t.newSubjectEngines(opts.AnalysisProgress)
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	// First match wins; duplicate symbols occur only in hand-edited
	// documents.
	prior := map[string]*Finding{}
	for i := range opts.Prior {
		f := &opts.Prior[i]
		if _, ok := prior[f.Symbol]; !ok {
			prior[f.Symbol] = f
		}
	}

	// Phase one, sequential: resolve every target to a terminal finding
	// (skipped, cached) or to a mutant work list.
	// Findings are keyed by symbol (REQ-result-record): two targets naming
	// one symbol would collide in the document, so the set is refused up
	// front rather than one silently shadowing the other.
	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg.Symbol] {
			return nil, fmt.Errorf("gomutant: duplicate target symbol %s", tg.Symbol)
		}
		seen[tg.Symbol] = true
	}

	findings := make([]Finding, len(targets))
	var pending []work
	type resolvedTarget struct {
		index  int
		oracle []string
	}
	var resolvedTargets []resolvedTarget
	var subjectSymbols []string
	type baselineKey struct {
		pkg, run, flags, moduleDir, packageDir string
	}
	baselineCache := map[baselineKey]runtimeinput.Observation{}
	coverageCache := map[string]engine.Coverage{}
	guidanceCache := map[string]oracleAttribution{}
	decisions := make([]RunDecision, len(targets))
	for i, tg := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationResolving, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Labels: tg.Labels, OperatorSet: engine.OperatorSet, OracleExplicit: tg.OracleExplicit || len(tg.Oracle) != 0, OracleTimeout: opts.OracleTimeout.String()}
		oracle, err := preparation.oracle(ctx, tg)
		if err != nil {
			return nil, err
		}
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default).
			f.Skipped = "no oracle"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		if err := preparation.validateOracle(ctx, oracle); err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		bodyHash, err := t.eng.BodyHashContext(ctx, tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			f.Skipped = "not a function - for mutation adequacy, target its methods or the bound function-level subjects"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		f.BodyHash = bodyHash
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationFreshness, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle})
		subjectSymbols = append(subjectSymbols, tg.Symbol)
		subjectSymbols = append(subjectSymbols, oracle...)
	}
	views := &subjectViewSet{bySymbol: map[string]*subjectView{}}
	if len(subjectSymbols) != 0 {
		var err error
		views, err = t.newSubjectViewsWithPackageContext(ctx, subjectSymbols, preparation.packageContext, false, engines)
		if err != nil {
			return nil, fmt.Errorf("freshness: %w", err)
		}
	}
	// One observed union over every target and oracle replaces the
	// per-target proof builds the campaign previously paid (the measured
	// ~270 observation passes per warm campaign): per-subject evidence is
	// identical by gofresh's batch-equivalence contract, per-symbol
	// faults stay target-local, and the per-target build survives only
	// as the bounded retry (REQ-exec-quiescence). The union is built at
	// the first target that needs a proof: a fully-cached warm run —
	// every target served — pays no observation pass at all.
	producerUnion := &subjectViewSet{bySymbol: map[string]*subjectView{}}
	producerFaults := map[string]error{}
	producerUnionBuilt := false
	buildProducerUnion := func() error {
		if producerUnionBuilt {
			return nil
		}
		producerUnionBuilt = true
		if opts.proofAttempt != nil {
			opts.proofAttempt("", 1)
		}
		var err error
		producerUnion, producerFaults, err = t.newObservedUnionViews(ctx, subjectSymbols, preparation.packageContext, engines)
		if err != nil {
			return fmt.Errorf("freshness proofs (union over %d subjects): %w", len(subjectSymbols), err)
		}
		return nil
	}
	var oraclePackages []string
	seenOraclePackage := map[string]bool{}
	for _, resolved := range resolvedTargets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, run := range pkgRuns(resolved.oracle) {
			if !seenOraclePackage[run.pkg] {
				seenOraclePackage[run.pkg] = true
				oraclePackages = append(oraclePackages, run.pkg)
			}
		}
	}
	for _, resolved := range resolvedTargets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i := resolved.index
		tg := targets[i]
		f := &findings[i]
		oracle := resolved.oracle
		targetView := views.bySymbol[tg.Symbol]
		oracleViews := make([]*subjectView, 0, len(oracle))
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, views.bySymbol[symbol])
		}
		rec, hasPrior := prior[tg.Symbol]
		reason := "no-prior"
		if hasPrior {
			switch {
			case opts.Force:
				reason = "forced"
			case !budgetCovers(*rec, opts.Budget):
				reason = fmt.Sprintf("budget: prior generated %d of %d candidates, request wants budget %d", rec.Generated, rec.CandidateCount, opts.Budget)
			default:
				reason = "stale"
			}
		}
		var serve *Finding
		var grow *Finding
		var growAdded []string
		var drift *Finding
		var driftMoved []string
		if hasPrior && !opts.Force && budgetCovers(*rec, opts.Budget) {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String())
			if err != nil {
				return nil, err
			}
			if !matches {
				// A mismatch may be exactly the growth the third carve-out
				// serves: the derived oracle grew while the compartment
				// moved by an inert declaration delta (REQ-result-stale).
				added, grows, gerr := evidenceSetCoversGrowthContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String())
				if gerr != nil {
					return nil, gerr
				}
				if grows {
					snapshot := snapshotFindings([]Finding{*rec})[0]
					grow = &snapshot
					growAdded = added
				} else if moved, drifts, derr := evidenceSetCoversKillerDriftContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String()); derr != nil {
					return nil, derr
				} else if drifts {
					// The compartment moved but the movement is attributable:
					// kills keyed to unmoved oracles stand, the rest
					// re-measures (REQ-result-stale's killer-drift carve-out).
					snapshot := snapshotFindings([]Finding{*rec})[0]
					drift = &snapshot
					driftMoved = moved
				} else {
					// The moved pin is named so a caller who just wrote
					// kill-tests sees the tool noticing them instead of
					// forcing defensively (REQ-result-stale). The class comes
					// from the inspection, not an assumed "stale": an
					// unverifiable prior is not stale.
					reason = t.movedPinAttribution(ctx, *rec, views, "stale: a measurement pin moved (oracle timeout, oracle selection, operator set, or runtime inputs moved during evaluation)")
				}
			}
			if matches && len(rec.CandidateEvidence) == 0 {
				cached := *rec
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
				// Matched pins make the current view's ledger identical to
				// the record's; stamping it is the conformance upgrade for a
				// record that predates the ledger (REQ-result-record).
				if ledger, lerr := targetView.view.TestVariantLedger(targetView.subject); lerr == nil {
					cached.CompartmentLedger = compartmentLedgerFromView(ledger)
				} else {
					return nil, lerr
				}
				findings[i] = cached
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: "served: body, oracle closure, and runtime inputs unchanged"}
				if err := commitFinding(ctx, repository, opts.Commit, cached); err != nil {
					return nil, err
				}
				continue
			}
			if matches {
				// The record's only unverifiable runtime evidence is
				// candidate-local: serve its covered candidates and
				// re-execute exactly the flagged ones under a passing
				// current baseline probe (REQ-result-stale). Candidate
				// regeneration below decides whether the splice can proceed.
				snapshot := snapshotFindings([]Finding{*rec})[0]
				serve = &snapshot
			}
		}
		var extend *Finding
		if hasPrior && !opts.Force && !budgetCovers(*rec, opts.Budget) {
			if len(rec.CandidateEvidence) != 0 {
				// The budget extension never composes with the
				// candidate-evidence serve: the flagged splice re-identifies
				// candidates under the record's own selection, so a wider
				// request re-measures whole rather than mixing the two
				// disciplines (REQ-result-stale).
				reason += "; prior candidate evidence re-executes only under its recorded budget, so the whole target re-measures"
			} else {
				matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String())
				if err != nil {
					return nil, err
				}
				if matches {
					// Every pin except the budget holds, so the record's
					// measured prefix stands as exact evidence for candidates
					// [0, generated) and only the unmeasured suffix executes
					// (REQ-mut-budget, REQ-result-stale's budget-extension
					// carve-out). Candidate regeneration below decides whether
					// the prefix re-identifies.
					snapshot := snapshotFindings([]Finding{*rec})[0]
					extend = &snapshot
				} else {
					// The budget was not the only moved pin, so the prefix
					// cannot stand; the appended attribution keeps the
					// re-measure reason honest (REQ-result-stale).
					reason += "; " + t.movedPinAttribution(ctx, *rec, views, "a measurement pin also moved, so the measured prefix cannot stand")
				}
			}
		}

		if err := buildProducerUnion(); err != nil {
			return nil, err
		}
		producerViews, err := producerUnion.forTarget(tg.Symbol, oracle, producerFaults)
		if err != nil && ctx.Err() == nil {
			// One bounded retry (REQ-exec-quiescence): the field failure
			// mode is transient pressure faulting one symbol or module
			// group out of the shared union, gone by the time anyone
			// reads the fault. The retry rebuilds this target's own proof
			// surface — the demoted per-target build — so a transient
			// union fault costs one extra pass for one target, never a
			// skip.
			if opts.proofAttempt != nil {
				opts.proofAttempt(tg.Symbol, 2)
			}
			producerViews, err = t.newSubjectViewsWithPackageContext(ctx, append([]string{tg.Symbol}, oracle...), preparation.packageContext, true, engines)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The campaign itself is canceled: abort is the answer.
				return nil, proofAbortError(tg.Symbol, oracle, err, ctxErr)
			}
			// A per-target evidence condition, target-local by the same
			// rule as drift refusal (REQ-exec-quiescence): this target
			// skips with the cause on its decision line — a skip never
			// overwrites a prior record — and the campaign proceeds.
			f.Skipped = fmt.Sprintf("freshness proof unavailable (oracle %s): %v", strings.Join(oracle, ", "), err)
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		// Producer enrollment happens only for targets whose proof
		// exists: enrolling before the build would put a skipped
		// target's module into end-of-run producer validation, turning a
		// target-local condition into a campaign-level drift report; a
		// shared oracle module is enrolled by the measured sibling
		// itself.
		targetView.module.producer = true
		for _, oracleView := range oracleViews {
			oracleView.module.producer = true
		}
		if opts.producer != nil {
			opts.producer(tg.Symbol)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetView = producerViews.bySymbol[tg.Symbol]
		oracleViews = oracleViews[:0]
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		for _, module := range producerViews.modules {
			module.producer = true
		}
		currentLedger, err := targetView.view.TestVariantLedger(targetView.subject)
		if err != nil {
			return nil, err
		}
		pending = append(pending, work{target: i, oracle: oracle, reason: reason, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, producer: producerViews, currentLedger: currentLedger, serve: serve, extend: extend, grow: grow, growAdded: growAdded, drift: drift, driftMoved: driftMoved})
	}
	for wi := range pending {
		w := &pending[wi]
		tg := targets[w.target]
		f := &findings[w.target]
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		budget := opts.Budget
		if w.serve != nil {
			// Regenerate the served record's exact selected prefix so its
			// flagged candidates re-identify deterministically.
			budget = w.serve.Budget
		}
		if w.grow != nil {
			// Same discipline for growth: the record's own selection is what
			// its survivors re-identify against — a covering but different
			// request budget must not manufacture a refusal that would
			// destroy a serviceable record (REQ-result-stale's growth
			// carve-out); the grown record keeps its recorded budget, which
			// covers the request by the arm's own gate.
			budget = w.grow.Budget
		}
		if w.drift != nil {
			// Same discipline again for killer drift: the record's own
			// selection is what its kills and survivors re-identify against
			// (REQ-result-stale's killer-drift carve-out).
			budget = w.drift.Budget
		}
		generation, err := t.eng.CandidatesContext(ctx, tg.Symbol, budget)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		if w.serve != nil {
			if flagged, ok := flaggedCandidateIndexes(generation, *w.serve); ok {
				w.candidates = generation.Candidates
				w.flagged = flagged
				decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: fmt.Sprintf("served: pins unchanged; re-executing %s", candidateNoun(len(flagged))), Candidates: len(flagged)}
			} else {
				// Deterministic regeneration cannot re-identify every flagged
				// candidate and recorded survivor, so the record cannot be
				// spliced: the whole target re-measures (REQ-result-stale).
				w.serve, w.flagged = nil, nil
				if budget != opts.Budget {
					generation, err = t.eng.CandidatesContext(ctx, tg.Symbol, opts.Budget)
					if err != nil {
						return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
					}
				}
			}
		}
		if w.extend != nil {
			if extendedPrefixStands(generation, *w.extend) {
				w.candidates = generation.Candidates
				w.extendFrom = w.extend.Generated
				suffix := len(generation.Candidates) - w.extendFrom
				decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: fmt.Sprintf("served: prefix of %s stands; measuring %d more", candidateNoun(w.extendFrom), suffix), Candidates: suffix}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// prefix, so the record cannot be extended: the whole target
				// re-measures (REQ-result-stale).
				w.extend = nil
				w.reason += "; deterministic regeneration cannot re-identify the measured prefix"
			}
		}
		if w.grow != nil {
			if survivors, ok := grownSurvivorIndexes(generation, *w.grow); ok {
				w.candidates = generation.Candidates
				w.growSurvivors = survivors
				decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "measure",
					Reason:     fmt.Sprintf("served: derived oracle grew by %s; re-measuring %s against them", testNoun(len(w.growAdded)), survivorNoun(len(survivors))),
					Candidates: len(survivors)}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// candidates and survivors, so the record cannot grow: the
				// whole target re-measures under the request's own budget,
				// with the reason naming what actually happened — the record
				// is not stale, its oracle grew (REQ-result-stale).
				w.grow = nil
				w.reason = fmt.Sprintf("derived oracle grew by %s, but deterministic regeneration cannot re-identify the recorded candidates and survivors", testNoun(len(w.growAdded)))
				if budget != opts.Budget {
					generation, err = t.eng.CandidatesContext(ctx, tg.Symbol, opts.Budget)
					if err != nil {
						return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
					}
				}
			}
		}
		if w.drift != nil {
			if remeasure, stand, ok := driftRemeasureIndexes(generation, *w.drift, w.driftMoved); ok {
				w.candidates = generation.Candidates
				w.driftRemeasure = remeasure
				reason := fmt.Sprintf("served: %s stand on unmoved oracles; re-measuring %s against the current oracle", killNoun(stand), candidateNoun(len(remeasure)))
				if len(w.driftMoved) == 0 {
					reason = "served: compartment delta reaches no recorded oracle; nothing re-measures"
				}
				decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason, Candidates: len(remeasure)}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// candidates, kills, and survivors, so the record cannot
				// serve under drift: the whole target re-measures under the
				// request's own budget (REQ-result-stale).
				w.drift = nil
				w.reason = "killer drift is attributable, but deterministic regeneration cannot re-identify the recorded candidates, kills, and survivors"
				if budget != opts.Budget {
					generation, err = t.eng.CandidatesContext(ctx, tg.Symbol, opts.Budget)
					if err != nil {
						return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
					}
				}
			}
		}
		if w.serve == nil && w.extend == nil && w.grow == nil && w.drift == nil {
			w.candidates = generation.Candidates
			decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: w.reason, Candidates: len(generation.Candidates)}
			f.Budget = opts.Budget
			f.CandidateCount = generation.CandidateCount
			f.Generated = len(generation.Candidates)
		}
		// Per-package oracle scoping (REQ-exec-oracle-run), with the rapid
		// failfile flag only in front of binaries that register it
		// (REQ-mut-overlay). A growth serve builds its groups over the added
		// tests alone — the recorded kills already rest on the recorded set
		// (REQ-core-attributed-kills) — each delta group earning its own
		// baseline below, so a failing added test refuses the run.
		if opts.PlanOnly {
			// The plan needs candidate counts and decisions, never
			// baseline probes: group construction and probing are
			// execution cost the plan exists to preview.
			continue
		}
		groupOracle := w.oracle
		if w.grow != nil {
			groupOracle = w.growAdded
		}
		runs := pkgRuns(groupOracle)
		rapid, err := preparation.rapidPackages(ctx, oraclePackages)
		if err != nil {
			return nil, err
		}
		for _, pr := range runs {
			var flags []string
			if rapid[pr.pkg] {
				flags = []string{"-rapid.nofailfile"}
			}
			moduleDir, packageDir, err := preparation.packageContext(ctx, pr.pkg)
			if err != nil {
				return nil, err
			}
			w.groups = append(w.groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		w.baselines = make([]runtimeinput.Observation, 0, len(w.groups))
		for _, group := range w.groups {
			key := baselineKey{pkg: group.pkgs[0], run: group.runRegex, flags: strings.Join(group.flags, "\x00"), moduleDir: group.moduleDir, packageDir: group.packageDir}
			state, ok := baselineCache[key]
			if !ok {
				reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0]})
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				ran, passed, observed, err := engine.TestProbeObservedEnv(ctx, t.dir, group.pkgs[0], group.runRegex, opts.OracleTimeout, group.flags, group.moduleDir, group.packageDir, opts.BracketPaths, runEnv)
				if err != nil {
					return nil, fmt.Errorf("target %s oracle baseline: %w", tg.Symbol, err)
				}
				if ran == 0 {
					return nil, fmt.Errorf("target %s oracle baseline matched no tests in %s", tg.Symbol, group.pkgs[0])
				}
				if !passed {
					return nil, fmt.Errorf("target %s oracle baseline does not pass in %s", tg.Symbol, group.pkgs[0])
				}
				state = observed
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				baselineCache[key] = state
			}
			w.baselines = append(w.baselines, state)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Decision != nil {
		for _, decision := range decisions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			opts.Decision(decision)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	if opts.PlanOnly {
		// A plan is a decision about committing budget, so it refuses
		// on the same tree-motion evidence an executing run's epilogue
		// checks — neither is a baseline probe or a mutant execution.
		if err := views.validateProducers(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &TreeDriftError{Transient: err.Error()}
		}
		if moved, err := repository.headMovedContext(ctx); err != nil {
			return nil, err
		} else if moved {
			return nil, fmt.Errorf("gomutant: repository HEAD moved during mutation run")
		}
		// Only findings complete without execution return (cached
		// serves and skips), so a partially-enumerated measure target
		// never escapes as evidence.
		var planned []Finding
		for i := range findings {
			if findings[i].Cached || findings[i].Skipped != "" {
				planned = append(planned, findings[i])
			}
		}
		return planned, nil
	}

	// Phase two: the pool. Outcomes land in a preallocated matrix so
	// aggregation is deterministic regardless of completion order; the first
	// error cancels everything in flight.
	outcomes := make([][]engine.MutantOutcome, len(pending))
	observations := make([][]runtimeinput.Observation, len(pending))
	incompletes := make([][]string, len(pending))
	killers := make([][]string, len(pending))
	for wi := range pending {
		outcomes[wi] = make([]engine.MutantOutcome, len(pending[wi].candidates))
		observations[wi] = make([]runtimeinput.Observation, len(pending[wi].candidates))
		incompletes[wi] = make([]string, len(pending[wi].candidates))
		killers[wi] = make([]string, len(pending[wi].candidates))
	}
	interner := &manifestInterner{byDigest: map[string]string{}}
	type job struct{ wi, mi int }
	// Execution proceeds in bounded target windows: each window's pool
	// drains, its kills confirm serially against an idle pool, and its
	// findings aggregate and COMMIT before the next window dispatches
	// (REQ-exec-cancellation's incremental-commit clause). One
	// campaign-global pool would hold every target's observations
	// resident until the end and commit nothing for hours, so a single
	// late abort cost the whole campaign's verdicts. Serial
	// confirmation's isolation contract is "alone after the pool
	// drains", which the per-window drain preserves.
	windowBudget := jobs * 8
	if windowBudget < 64 {
		windowBudget = 64
	}
	if runWindowCandidates > 0 {
		windowBudget = runWindowCandidates
	}
	var treeDrift error
	var drifted []TargetDrift
	// commitAndAttribute is the one epilogue every measured or spliced
	// finding leaves aggregation through: install, persist, and — when
	// the evidence landed unverifiable under a package-derived oracle —
	// emit the oracle-instability attribution
	// (REQ-exec-oracle-guidance). One exit keeps the attribution
	// structurally coupled to the commit: no serve arm can persist an
	// unverifiable record silently.
	commitAndAttribute := func(ctx context.Context, f Finding, w work) error {
		findings[w.target] = f
		if err := commitFinding(ctx, repository, opts.Commit, f); err != nil {
			return err
		}
		return t.emitOracleGuidance(ctx, f, w, targets[w.target].Symbol, opts, runEnv, guidanceCache)
	}
	// Advisory execution progress rides window boundaries: totals are
	// exact, per-window timing is not part of the deterministic sequence
	// (REQ-exec-run-status's advisory classes).
	mutantsTotal, mutantsDone := 0, 0
	for wi := range pending {
		mutantsTotal += len(pending[wi].candidates)
	}
	for windowStart := 0; windowStart < len(pending); {
		windowEnd := windowStart
		for budget := 0; windowEnd < len(pending) && (budget == 0 || budget < windowBudget); windowEnd++ {
			budget += len(pending[windowEnd].candidates)
		}
		reportExecuting(opts.Executing, ExecutionEvent{
			Phase:       "executing",
			TargetIndex: windowStart + 1, TargetCount: len(pending),
			Symbol:         targets[pending[windowStart].target].Symbol,
			CandidatesDone: mutantsDone, CandidatesTotal: mutantsTotal,
		})
		jobCh := make(chan job)
		poolCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		var errOnce sync.Once
		var poolErr error
		for range jobs {
			if err := poolCtx.Err(); err != nil {
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					if poolCtx.Err() != nil {
						return
					}
					w := pending[j.wi]
					m, runnable := w.candidates[j.mi].Mutant()
					if !runnable {
						continue
					}
					outcome, killer, state, incompleteReason, err := t.executeMutant(poolCtx, w, m, opts, runEnv)
					if err != nil {
						if poolCtx.Err() != nil {
							return
						}
						errOnce.Do(func() {
							poolErr = err
							cancel()
						})
						return
					}
					observations[j.wi][j.mi] = interner.intern(state)
					incompletes[j.wi][j.mi] = incompleteReason
					killers[j.wi][j.mi] = killer
					outcomes[j.wi][j.mi] = outcome
				}
			}()
		}
	dispatching:
		for wi := windowStart; wi < windowEnd; wi++ {
			for mi, candidate := range pending[wi].candidates {
				if _, runnable := candidate.Mutant(); !runnable {
					continue
				}
				if pending[wi].serve != nil && !pending[wi].flagged[mi] {
					// A served record's covered candidates keep their recorded
					// outcomes; only the flagged ones re-execute
					// (REQ-result-stale).
					continue
				}
				if pending[wi].extend != nil && mi < pending[wi].extendFrom {
					// An extended record's measured prefix keeps its recorded
					// outcomes; only the unmeasured suffix executes
					// (REQ-mut-budget, REQ-result-stale's budget-extension
					// carve-out).
					continue
				}
				if pending[wi].grow != nil && !pending[wi].growSurvivors[mi] {
					// A grown record's kills and discards stand — a grown oracle
					// can only kill more — so only the recorded survivors
					// re-execute, against the added tests alone
					// (REQ-result-stale's growth carve-out).
					continue
				}
				if pending[wi].drift != nil && !pending[wi].driftRemeasure[mi] {
					// A drifted record's kills keyed to unmoved oracles and its
					// discards stand; only moved-killer kills, set-wide kills
					// under any movement, and survivors re-execute, against the
					// full current oracle (REQ-result-stale's killer-drift
					// carve-out).
					continue
				}
				if opts.dispatched != nil {
					opts.dispatched(pending[wi].candidates[mi].Symbol, mi)
				}
				select {
				case jobCh <- job{wi, mi}:
				case <-poolCtx.Done():
					break dispatching
				}
			}
		}
		close(jobCh)
		wg.Wait()
		if poolErr != nil {
			return nil, poolErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Serial kill confirmation (REQ-exec-attribution): a test-attributed
		// or package-scope kill measured beside sibling mutants re-executes
		// alone, and the serial execution is the scored one - outcome,
		// observation, and candidate evidence replaced wholesale - so
		// interference from a sibling never reads as a kill. Timeout kills
		// are excluded: confirming one costs the full timeout again, and the
		// hang bound is the caller's own budget - the named residual.
		for wi := windowStart; wi < windowEnd; wi++ {
			mutantsDone += len(pending[wi].candidates)
		}
		if jobs > 1 {
			// Each confirmation re-runs a full oracle, so per-confirmation
			// events are naturally sparse; a window with nothing to
			// confirm reports nothing.
			confirmTotal := 0
			for wi := windowStart; wi < windowEnd; wi++ {
				for mi := range pending[wi].candidates {
					if outcomes[wi][mi] == engine.MutantKilled && killers[wi][mi] != engine.TimeoutKiller {
						if _, runnable := pending[wi].candidates[mi].Mutant(); runnable {
							confirmTotal++
						}
					}
				}
			}
			confirmDone := 0
			var kills []windowKill
			for wi := windowStart; wi < windowEnd; wi++ {
				for mi := range pending[wi].candidates {
					if outcomes[wi][mi] != engine.MutantKilled || killers[wi][mi] == engine.TimeoutKiller {
						continue
					}
					if _, runnable := pending[wi].candidates[mi].Mutant(); !runnable {
						continue
					}
					kills = append(kills, windowKill{target: wi, mi: mi})
				}
			}
			// Volatile evidence anywhere in a target's window — the
			// baseline or any mutant observation — is load sensitivity's
			// signature: that target's gate never samples. Snapshots are
			// per target at walk start; serial evidence arriving
			// unverifiable re-arms mid-walk through the walk's own
			// callback.
			volatileMemo := map[int]bool{}
			err := confirmWindowKills(
				func(wi int) bool {
					v, ok := volatileMemo[wi]
					if !ok {
						v = windowEvidenceVolatile(&pending[wi], observations[wi])
						volatileMemo[wi] = v
					}
					return v
				},
				kills,
				func(k windowKill) (confirmOutcome, error) {
					if err := ctx.Err(); err != nil {
						return confirmInconclusive, err
					}
					wi := k.target
					m, _ := pending[wi].candidates[k.mi].Mutant()
					reportExecuting(opts.Executing, ExecutionEvent{
						Phase:       "confirming",
						TargetIndex: windowStart + 1, TargetCount: len(pending),
						Symbol:         targets[pending[wi].target].Symbol,
						CandidatesDone: mutantsDone, CandidatesTotal: mutantsTotal,
						ConfirmationsDone: confirmDone, ConfirmationsTotal: confirmTotal,
					})
					outcome, killer, state, incomplete, err := t.executeMutant(ctx, pending[wi], m, opts, runEnv)
					if err != nil {
						return confirmInconclusive, err
					}
					confirmDone++
					outcomes[wi][k.mi] = outcome
					killers[wi][k.mi] = killer
					observations[wi][k.mi] = interner.intern(state)
					incompletes[wi][k.mi] = incomplete
					return classifyConfirmation(outcome, killer), nil
				},
				func(k windowKill) bool {
					return observations[k.target][k.mi].Unverifiable
				},
			)
			if err != nil {
				return nil, err
			}
		}
		if windowEnd == len(pending) {
			if opts.afterExecution != nil {
				opts.afterExecution()
			}
			// A drifted producer view refuses target-locally, not
			// campaign-wide: the per-target validations below decide which
			// findings still bind, so a concurrent edit costs only the
			// affected targets (REQ-exec-quiescence). The campaign-wide
			// check runs once, after the last window's execution, exactly
			// as it did before windows existed; earlier windows' commits
			// are gated by their own per-target validations.
			if err := views.validateProducers(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				treeDrift = err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// Phase three, per window and sequential: aggregate in target and
		// mutant order and commit each finding before the next window
		// dispatches.
		for wi := windowStart; wi < windowEnd; wi++ {
			w := pending[wi]
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if opts.aggregate != nil {
				opts.aggregate()
			}
			f := &findings[w.target]
			if w.serve != nil {
				spliced, err := t.spliceServedFinding(ctx, runEnv, *w.serve, w.candidates, w.flagged, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels)
				if err != nil {
					return nil, err
				}
				// The aggregated work item's retained observations are dead past
				// this point; releasing them per item keeps the run's peak at the
				// in-flight items rather than the whole campaign.
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
					continue
				}
				if err := commitAndAttribute(ctx, spliced, w); err != nil {
					return nil, err
				}
				continue
			}
			if w.grow != nil {
				grown, union, shed, err := t.spliceGrownFinding(ctx, runEnv, *w.grow, w, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels)
				if err != nil {
					return nil, err
				}
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
					continue
				}
				// The grown record carries the current tree's evidence, so its
				// commit and dirty provenance are recomputed like a fresh
				// measure's rather than carried from the served record.
				if err := t.stampProvenance(ctx, repository, w, &grown, union); err != nil {
					return nil, err
				}
				// Advisory buckets re-derived honestly under the delta oracle:
				// an added test executing a previously never-executed survivor
				// upgrades its bucket; downgrades never happen — the recorded
				// bucket was measured under the full oracle — and a
				// divergence-stamped record's survivors were already classified
				// unstable by the counts fold.
				if !grown.TargetEvidence.RuntimeUnverifiable {
					coverage, probed, err := t.oracleCoverage(ctx, w, opts, runEnv, coverageCache)
					if err != nil {
						return nil, err
					}
					if probed {
						coverPkg := w.targetView.subject.Package
						for si := range grown.Survivors {
							if grown.Survivors[si].Execution == "executed-and-passed" {
								continue
							}
							file, line, col, ok := splitSurvivorPosition(grown.Survivors[si].Position)
							if ok && coverage.Covered(coverPkg+"/"+file, line, col) {
								grown.Survivors[si].Execution = "executed-and-passed"
							}
						}
					}
				}
				if err := commitAndAttribute(ctx, grown, w); err != nil {
					return nil, err
				}
				// Evidence beats attestation: each shed disposition names its
				// killer so the contradiction — a mutant judged equivalent was
				// just distinguished — reaches a human (REQ-attest-survivor,
				// REQ-result-stale's growth carve-out).
				if opts.Contradiction != nil && len(shed) != 0 {
					byIdentity, _ := candidateIdentityIndex(w.candidates)
					for _, attestation := range shed {
						killer := ""
						if mi, ok := byIdentity[survivorKey{attestation.Position, attestation.Operator}]; ok {
							killer = killers[wi][mi]
						}
						opts.Contradiction(AttestationContradiction{
							Symbol: targets[w.target].Symbol, Position: attestation.Position,
							Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
						})
					}
				}
				continue
			}
			if w.drift != nil {
				spliced, union, shed, err := t.spliceDriftFinding(ctx, runEnv, *w.drift, w, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels)
				if err != nil {
					return nil, err
				}
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
					continue
				}
				// The drifted record carries the current tree's evidence — the
				// gate proved the retained movement is the attributable
				// compartment delta — so provenance is recomputed like a fresh
				// measure's (REQ-result-stale's killer-drift carve-out).
				if err := t.stampProvenance(ctx, repository, w, &spliced, union); err != nil {
					return nil, err
				}
				// With any oracle moved, every surviving candidate was
				// re-measured against the full current oracle, so advisory
				// buckets re-derive from the current probe exactly as a
				// fresh measure's do; a no-reach serve re-measured nothing
				// and carries every recorded bucket verbatim, paying no
				// coverage probe.
				if len(w.driftMoved) != 0 && !spliced.TargetEvidence.RuntimeUnverifiable {
					if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, coverageCache, 0); err != nil {
						return nil, err
					}
				}
				if err := commitAndAttribute(ctx, spliced, w); err != nil {
					return nil, err
				}
				// Evidence beats attestation, exactly as under growth: a
				// re-measured attested survivor a moved test now kills sheds
				// its attestation with the contradiction reported.
				if opts.Contradiction != nil && len(shed) != 0 {
					byIdentity, _ := candidateIdentityIndex(w.candidates)
					for _, attestation := range shed {
						killer := ""
						if mi, ok := byIdentity[survivorKey{attestation.Position, attestation.Operator}]; ok {
							killer = killers[wi][mi]
						}
						opts.Contradiction(AttestationContradiction{
							Symbol: targets[w.target].Symbol, Position: attestation.Position,
							Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
						})
					}
				}
				continue
			}
			if w.extend != nil {
				extended, err := t.spliceExtendedFinding(ctx, runEnv, *w.extend, w.candidates, w.extendFrom, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Budget)
				if err != nil {
					return nil, err
				}
				// Same release as the served branch: the splice is computed, the
				// per-candidate observations are dead.
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
					continue
				}
				// Advisory execution buckets: a verifiable extension's suffix
				// survivors earn theirs from the current probe like any measured
				// run's, while carried prefix survivors keep their recorded
				// buckets verbatim; a divergence-stamped extension's suffix
				// survivors were already classified unstable by the splice.
				if !extended.TargetEvidence.RuntimeUnverifiable {
					if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, coverageCache, len(w.extend.Survivors)); err != nil {
						return nil, err
					}
				}
				if err := commitAndAttribute(ctx, extended, w); err != nil {
					return nil, err
				}
				continue
			}
			state, candidateEvidence, err := completedObservationUnion(ctx, t.dir, runEnv, w.baselines, w.candidates, outcomes[wi], observations[wi], incompletes[wi], nil)
			if err != nil {
				return nil, err
			}
			// Same release as the served branch: the union is computed, the
			// per-candidate observations are dead.
			observations[wi] = nil
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			f.CandidateEvidence = candidateEvidence
			targetEvidence, oracleEvidence, err := attachEvidence(w.targetView, w.oracleViews, state)
			if err != nil {
				return nil, err
			}
			f.TargetEvidence = targetEvidence
			f.OracleEvidence = oracleEvidence
			f.CompartmentLedger = compartmentLedgerFromView(w.currentLedger)
			if err := w.producer.validateProducers(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
				continue
			}
			if err := t.stampProvenance(ctx, repository, w, f, state); err != nil {
				return nil, err
			}
			f.Operators = summarizeOperators(w.candidates, outcomes[wi])
			for _, summary := range f.Operators {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				f.Discarded += summary.Discarded
				f.Mutants += summary.Killed + summary.Survived
				f.Killed += summary.Killed
			}
			for mi, candidate := range w.candidates {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				switch outcomes[wi][mi] {
				case engine.MutantSurvived:
					f.Survivors = append(f.Survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator})
				case engine.MutantKilled:
					// The keystone persisted: every kill names its killer
					// (REQ-core-attributed-kills), so reuse can key the kill
					// to its killer's content (REQ-result-stale's
					// killer-drift carve-out).
					f.Kills = append(f.Kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[wi][mi]})
				}
			}
			if err := t.bucketSurvivorExecution(ctx, f, w, opts, runEnv, coverageCache, 0); err != nil {
				return nil, err
			}
			// A re-measure with unchanged pins keeps prior attestations that
			// still name the exact survivor; changed pins shed them, so every
			// evidence version's equivalences are re-judged (REQ-attest-survivor).
			if rec, ok := prior[targets[w.target].Symbol]; ok && sameAttestationPins(*rec, *f) {
				open := map[survivorKey]bool{}
				for _, s := range f.Survivors {
					open[survivorKey{s.Position, s.Operator}] = true
				}
				for _, a := range rec.Attested {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if open[survivorKey{a.Position, a.Operator}] {
						f.Attested = append(f.Attested, a)
					}
				}
			}
			if err := commitAndAttribute(ctx, *f, w); err != nil {
				return nil, err
			}
		}
		cancel()
		windowStart = windowEnd
	}
	// A run with nothing pending — every target fully served, or no
	// targets at all — still owes the campaign epilogue: a transient
	// global drift no surviving target's evidence reflects is reported,
	// never silently absorbed (REQ-exec-quiescence).
	if len(pending) == 0 {
		if opts.afterExecution != nil {
			opts.afterExecution()
		}
		if err := views.validateProducers(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			treeDrift = err
		}
	}
	moved, err := repository.headMovedContext(ctx)
	if err != nil {
		return nil, err
	}
	if moved {
		return nil, fmt.Errorf("gomutant: repository HEAD moved during mutation run")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(drifted) > 0 || treeDrift != nil {
		completed := completedFindings(findings, drifted)
		if len(drifted) == 0 {
			return completed, &TreeDriftError{Completed: len(completed), Transient: treeDrift.Error()}
		}
		return completed, &TreeDriftError{Drifted: drifted, Completed: len(completed)}
	}
	return findings, nil
}

func snapshotTargets(targets []Target) []Target {
	snapshot := slices.Clone(targets)
	for i := range snapshot {
		snapshot[i].Oracle = slices.Clone(snapshot[i].Oracle)
		snapshot[i].Labels = slices.Clone(snapshot[i].Labels)
	}
	return snapshot
}

func snapshotFindings(findings []Finding) []Finding {
	snapshot := slices.Clone(findings)
	for i := range snapshot {
		snapshot[i] = cloneFinding(snapshot[i])
	}
	return snapshot
}

// runWindowCandidates overrides the execution window candidate budget
// when positive - a test seam; zero means the jobs-derived default.
var runWindowCandidates int

// commitFinding delivers one finished finding to the caller's incremental
// commit callback. The pre-delivery HEAD check mirrors the run's final one so
// a finding whose capture-commit provenance no longer names HEAD is never
// persisted incrementally: the run aborts exactly as it would at the end.
func commitFinding(ctx context.Context, repository repositoryState, commit func(Finding) error, f Finding) error {
	if commit == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	moved, err := repository.headMovedContext(ctx)
	if err != nil {
		return err
	}
	if moved {
		return fmt.Errorf("gomutant: repository HEAD moved during mutation run")
	}
	return commit(f)
}

func reportPreparation(callback func(PreparationEvent), event PreparationEvent) {
	if callback != nil {
		callback(event)
	}
}

// Confirmation stride gating (REQ-exec-attribution): after
// confirmStreak consecutive reproductions within one target's window,
// further kills confirm at every confirmStrideth candidate; any flip
// restores full confirmation retroactively. The constants realize the
// contract's "run of consecutive reproductions" and "fixed
// deterministic stride" — the spec deliberately leaves the numbers
// code-side (nothing persisted or wire-visible depends on them).
const (
	confirmStreak = 3
	confirmStride = 4
)

// confirmationGate is the per-target, per-window stride gate over
// serial kill confirmation. Deterministic by construction: its inputs
// are the candidate-ordered reproduction results, never worker timing.
// A volatile gate (unverifiable evidence in the window) never samples.
type confirmationGate struct {
	volatile    bool
	streak      int
	sinceSample int
	flipped     bool
}

// confirmNow reports whether the next kill confirms serially. A gate
// that is volatile, flipped, or still inside the opening streak always
// confirms; otherwise every confirmStrideth kill confirms and the rest
// stride-skip.
func (g *confirmationGate) confirmNow() bool {
	if g.volatile || g.flipped || g.streak < confirmStreak {
		return true
	}
	g.sinceSample++
	if g.sinceSample >= confirmStride {
		g.sinceSample = 0
		return true
	}
	return false
}

// confirmOutcome classifies one serial confirmation as gate evidence:
// a reproduction extends the streak, a flip is a demonstrated
// collision, and an inconclusive result — a serial timeout, excluded
// from confirmation in both directions — is no evidence either way.
type confirmOutcome int

const (
	confirmReproduced confirmOutcome = iota
	confirmFlipped
	confirmInconclusive
)

// observe records a serial confirmation's evidence and reports whether
// a flip just demanded retroactive confirmation of stride-skipped
// kills.
func (g *confirmationGate) observe(outcome confirmOutcome) (retroactive bool) {
	switch outcome {
	case confirmReproduced:
		g.streak++
		return false
	case confirmInconclusive:
		return false
	}
	first := !g.flipped
	g.flipped = true
	return first
}

func reportExecuting(callback func(ExecutionEvent), event ExecutionEvent) {
	if callback != nil {
		callback(event)
	}
}

func mergeFindingObservations(root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	return mergeFindingObservationsContext(context.Background(), root, env, states...)
}

func mergeFindingObservationsContext(ctx context.Context, root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, err
	}
	state, err := runtimeinput.MergeEnv(root, env, states...)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return state, nil
	}
	process := fmt.Sprintf("gomutant-finding-merge-%d", findingObservationSequence.Add(1))
	result, incompleteErr := runtimeinput.IncompleteEnv(root, process, "runtime input observations could not be merged for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	for _, input := range states {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if input.Manifest == "" {
			continue
		}
		merged, mergeErr := runtimeinput.MergeEnv(root, env, result, input)
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

// manifestInterner shares one backing string among a run's identical retained
// observation manifests. The digest is a content hash over exactly the entries
// the canonical encoding serializes, so equal digests name byte-identical
// manifest strings and the sharing is observationally invisible; what it
// removes is candidates-times-manifest retention, which has taken a measuring
// process to tens of gigabytes on document-heavy oracles whose mutants observe
// the same input sets.
type manifestInterner struct {
	mu       sync.Mutex
	byDigest map[string]string
}

func (in *manifestInterner) intern(o runtimeinput.Observation) runtimeinput.Observation {
	if o.Manifest == "" || o.Digest == "" {
		return o
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if canonical, ok := in.byDigest[o.Digest]; ok {
		o.Manifest = canonical
		return o
	}
	in.byDigest[o.Digest] = o.Manifest
	return o
}

// completedObservationUnion unions the finding-wide baseline observations with
// the completed candidate observations. A candidate whose process could not
// prove its runtime evidence sound is excluded from the union and returned as
// explicit candidate evidence carrying its incomplete-process reason and
// measured disposition instead (REQ-exec-observation; candidate evidence,
// REQ-result-record). Baseline observations are always finding-wide: an
// incomplete baseline observation leaves the union — and so the finding —
// unverifiable. A non-nil flagged set restricts the walk to those candidate
// indexes (the re-execution splice); nil walks every runnable candidate.
func completedObservationUnion(ctx context.Context, root string, env []string, baselines []runtimeinput.Observation, candidates []engine.Candidate, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, flagged map[int]bool) (runtimeinput.Observation, []CandidateEvidence, error) {
	states := append([]runtimeinput.Observation(nil), baselines...)
	var evidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, nil, err
		}
		if flagged != nil && !flagged[mi] {
			continue
		}
		if _, runnable := candidate.Mutant(); !runnable {
			continue
		}
		if reason := incompletes[mi]; reason != "" {
			evidence = append(evidence, CandidateEvidence{
				Position:    candidate.Position,
				Operator:    candidate.Operator,
				Reason:      reason,
				Disposition: outcomeDisposition(outcomes[mi]),
			})
			continue
		}
		states = append(states, observations[mi])
	}
	union, err := mergeFindingObservationsContext(ctx, root, env, states...)
	if err != nil {
		return runtimeinput.Observation{}, nil, err
	}
	return union, evidence, nil
}

func outcomeDisposition(outcome engine.MutantOutcome) string {
	switch outcome {
	case engine.MutantKilled:
		return "killed"
	case engine.MutantSurvived:
		return "survived"
	default:
		return "discarded"
	}
}

// emitOracleGuidance attributes oracle instability for a measured finding
// whose merged runtime evidence landed unverifiable under a package-derived
// oracle (REQ-exec-oracle-guidance). Targets sharing one oracle set share one
// attribution: the probes run once per set, not per finding. A budget
// extension owes the same attribution as a whole measure — its suffix
// processes are what landed the merged record unverifiable.
func (t *Tree) emitOracleGuidance(ctx context.Context, f Finding, w work, symbol string, opts Options, runEnv []string, guidanceCache map[string]oracleAttribution) error {
	if opts.Guidance == nil || !f.TargetEvidence.RuntimeUnverifiable || f.OracleExplicit {
		return nil
	}
	key := strings.Join(slices.Sorted(slices.Values(w.oracle)), "\x00")
	attr, ok := guidanceCache[key]
	if !ok {
		probed, gerr := t.probeOracleInstability(ctx, w.oracle, w.groups, opts, runEnv)
		if gerr != nil {
			return gerr
		}
		attr = probed
		guidanceCache[key] = attr
	}
	opts.Guidance(buildOracleGuidance(symbol, f.TargetEvidence.RuntimeReason, w.oracle, attr))
	return nil
}

// stampProvenance records the current tree's provenance on a measured or
// grown finding: the capture commit, and dirty computed over the finding's
// subject source files, their historical package files, module selection,
// and workspace inputs, against the observation's runtime state.
func (t *Tree) stampProvenance(ctx context.Context, repository repositoryState, w work, f *Finding, observation runtimeinput.Observation) error {
	f.Commit = repository.commit
	sourceFiles := append([]string(nil), w.targetView.sourceFiles...)
	for _, oracleView := range w.oracleViews {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourceFiles = append(sourceFiles, oracleView.sourceFiles...)
	}
	historical, err := repository.historicalPackageFilesContext(ctx, sourceFiles)
	if err != nil {
		return err
	}
	sourceFiles = append(sourceFiles, historical...)
	sourceFiles = withModuleSelectionPaths(sourceFiles)
	if err := ctx.Err(); err != nil {
		return err
	}
	sourceFiles = append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum"))
	f.Dirty, err = repository.pathsDirtyContext(ctx, sourceFiles, observation.State)
	return err
}

// spliceGrownFinding serves a record under the oracle-growth carve-out
// (REQ-result-stale): recorded kills and discards stand, the re-executed
// survivors' fresh outcomes replace their dispositions, and the grown record
// carries the current tree's evidence for every subject — the gate proved
// the retained subjects' only movement is the inert compartment delta, and
// the delta run captured the added oracles' evidence — plus the current
// compartment ledger. The delta processes' completed union is reconciled
// over the record's persisted union exactly as the budget extension's: a
// read beyond the recorded pins preserves the grown outcome but stamps it
// explicitly non-reusable. Returns the reconciled union (for provenance)
// and the shed attestations of newly killed survivors.
func (t *Tree) spliceGrownFinding(ctx context.Context, env []string, rec Finding, w work, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string) (Finding, runtimeinput.Observation, []Attestation, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, w.candidates, w.growSurvivors, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, runtimeinput.Observation{}, nil, err
	}
	rec = spliced.rec
	// The grown record carries the current tree's evidence for every
	// subject — the gate proved the retained subjects' only movement is the
	// inert compartment delta, and the delta run captured the added
	// oracles' — with a divergence stamp transferred onto it.
	diverged := rec.TargetEvidence.RuntimeUnverifiable
	divergenceReason := rec.TargetEvidence.RuntimeReason
	rec.TargetEvidence = spliced.targetEvidence
	rec.OracleEvidence = spliced.oracleEvidence
	if diverged {
		rec.TargetEvidence.RuntimeUnverifiable, rec.TargetEvidence.RuntimeReason = true, divergenceReason
		for i := range rec.OracleEvidence {
			rec.OracleEvidence[i].RuntimeUnverifiable, rec.OracleEvidence[i].RuntimeReason = true, divergenceReason
		}
	}
	grown, shed, err := growFindingCounts(ctx, rec, w.candidates, w.growSurvivors, outcomes, killers, spliced.fresh)
	return grown, spliced.union, shed, err
}

// growFindingCounts replaces each re-executed survivor's disposition with
// its fresh outcome — per operator and in the finding totals — while every
// other candidate keeps its recorded one (INV-RESULT-CANDIDATE-CONSERVATION;
// the growth serve re-executes exactly the recorded survivors). A survivor
// surviving the added tests keeps its recorded advisory bucket (upgraded by
// the caller's delta coverage probe), classifying unstable-oracle when the
// spliced evidence landed non-reusable; a newly killed attested survivor's
// attestation is shed and returned — evidence beats attestation
// (REQ-attest-survivor).
func growFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, survivors map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence) (Finding, []Attestation, error) {
	// Kill attribution is maintained only over a record that carries it
	// completely; a record predating attribution stays without one and
	// re-measures whole under the killer-drift carve-out
	// (REQ-core-attributed-kills, REQ-result-stale).
	killsComplete := len(rec.Kills) == rec.Killed
	kills := append([]Kill(nil), rec.Kills...)
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	priorBuckets := make(map[survivorKey]string, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorBuckets[survivorKey{survivor.Position, survivor.Operator}] = survivor.Execution
	}
	stamp := rec.TargetEvidence.RuntimeUnverifiable
	var stillSurviving []Survivor
	for mi := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, nil, err
		}
		if !survivors[mi] {
			continue
		}
		candidate := candidates[mi]
		i, ok := byOperator[candidate.Operator]
		if !ok {
			return Finding{}, nil, fmt.Errorf("gomutant: grown survivor %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		applyDisposition(&operators[i], "survived", -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			execution := priorBuckets[survivorKey{candidate.Position, candidate.Operator}]
			if stamp {
				execution = "unstable-oracle"
			}
			stillSurviving = append(stillSurviving, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: execution})
		case "killed":
			if killsComplete {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
			}
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, nil, fmt.Errorf("gomutant: grown operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = stillSurviving
	rec.CandidateEvidence = freshEvidence
	open := make(map[survivorKey]bool, len(stillSurviving))
	for _, survivor := range stillSurviving {
		open[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	var shed []Attestation
	for _, attestation := range rec.Attested {
		if open[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		} else {
			shed = append(shed, attestation)
		}
	}
	rec.Attested = kept
	return rec, shed, nil
}

// spliceDriftFinding serves a record under the killer-drift carve-out
// (REQ-result-stale): kills keyed to unmoved oracles and all discards keep
// their recorded dispositions, the re-measured candidates' fresh outcomes —
// measured against the full current oracle — replace theirs, and the drifted
// record carries the current tree's evidence for every subject plus the
// current compartment ledger, exactly the growth serve's discipline. The
// re-measure processes' completed union is reconciled over the record's
// persisted union fail-closed: a read beyond the recorded pins preserves the
// drifted outcome but stamps it explicitly non-reusable. Returns the
// reconciled union (for provenance) and the shed attestations of newly
// killed survivors.
func (t *Tree) spliceDriftFinding(ctx context.Context, env []string, rec Finding, w work, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string) (Finding, runtimeinput.Observation, []Attestation, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, w.candidates, w.driftRemeasure, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, runtimeinput.Observation{}, nil, err
	}
	rec = spliced.rec
	diverged := rec.TargetEvidence.RuntimeUnverifiable
	divergenceReason := rec.TargetEvidence.RuntimeReason
	rec.TargetEvidence = spliced.targetEvidence
	rec.OracleEvidence = spliced.oracleEvidence
	if diverged {
		rec.TargetEvidence.RuntimeUnverifiable, rec.TargetEvidence.RuntimeReason = true, divergenceReason
		for i := range rec.OracleEvidence {
			rec.OracleEvidence[i].RuntimeUnverifiable, rec.OracleEvidence[i].RuntimeReason = true, divergenceReason
		}
	}
	driftedFinding, shed, err := driftFindingCounts(ctx, rec, w.candidates, w.driftRemeasure, outcomes, killers, spliced.fresh)
	return driftedFinding, spliced.union, shed, err
}

// driftFindingCounts replaces each re-measured candidate's disposition with
// its fresh outcome — per operator and in the finding totals — while every
// standing candidate keeps its recorded one
// (INV-RESULT-CANDIDATE-CONSERVATION). The kill list is rebuilt in candidate
// order: standing kills carry their recorded killer, re-measured candidates
// that die again (or a re-measured survivor a moved test now kills) record
// their fresh killer. Survivors are rebuilt fresh — every recorded survivor
// was re-measured against the full current oracle — classifying
// unstable-oracle when the spliced evidence landed non-reusable; a newly
// killed attested survivor's attestation is shed and returned — evidence
// beats attestation (REQ-attest-survivor).
func driftFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, remeasured map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence) (Finding, []Attestation, error) {
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	recordedKills := make(map[survivorKey]string, len(rec.Kills))
	for _, kill := range rec.Kills {
		recordedKills[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	recordedSurvivors := make(map[survivorKey]Survivor, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		recordedSurvivors[survivorKey{survivor.Position, survivor.Operator}] = survivor
	}
	stamp := rec.TargetEvidence.RuntimeUnverifiable
	var survivors []Survivor
	var kills []Kill
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, nil, err
		}
		key := survivorKey{candidate.Position, candidate.Operator}
		if !remeasured[mi] {
			if killer, ok := recordedKills[key]; ok {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killer})
			}
			if prior, ok := recordedSurvivors[key]; ok {
				// A standing survivor — nothing moved, so its survival
				// stands exactly like a standing kill — carries its
				// recorded advisory bucket verbatim
				// (REQ-exec-survivor-evidence).
				survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: prior.Execution})
			}
			continue
		}
		i, ok := byOperator[candidate.Operator]
		if !ok {
			return Finding{}, nil, fmt.Errorf("gomutant: drifted candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		_, wasSurvivor := recordedSurvivors[key]
		var recordedDisposition string
		switch {
		case recordedKills[key] != "":
			recordedDisposition = "killed"
		case wasSurvivor:
			recordedDisposition = "survived"
		default:
			return Finding{}, nil, fmt.Errorf("gomutant: drifted candidate %s %s has no recorded disposition", candidate.Position, candidate.Operator)
		}
		applyDisposition(&operators[i], recordedDisposition, -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			execution := ""
			if stamp {
				execution = "unstable-oracle"
			}
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: execution})
		case "killed":
			kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, nil, fmt.Errorf("gomutant: drifted operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	if len(kills) != killed {
		// A corrupt prior — a kill attribution naming a candidate the
		// record discarded — would otherwise persist a document whose next
		// parse refuses it; aborting keeps the refusal at the source.
		return Finding{}, nil, fmt.Errorf("gomutant: drifted kill attributions (%d) do not cover the killed count (%d)", len(kills), killed)
	}
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = kills
	rec.Survivors = survivors
	rec.CandidateEvidence = freshEvidence
	open := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		open[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	var shed []Attestation
	for _, attestation := range rec.Attested {
		if open[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		} else {
			shed = append(shed, attestation)
		}
	}
	rec.Attested = kept
	return rec, shed, nil
}

// oracleCoverage merges the baseline coverage probes of a work item's oracle
// groups over the target's package, cached per (package, run pattern, cover
// package) across the run; probed reports false when any group cannot be
// probed (best-effort advisory data, never a refusal).
func (t *Tree) oracleCoverage(ctx context.Context, w work, opts Options, runEnv []string, cache map[string]engine.Coverage) (engine.Coverage, bool, error) {
	coverPkg := w.targetView.subject.Package
	coverage := engine.Coverage{}
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return engine.Coverage{}, false, err
		}
		key := g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg
		got, ok := cache[key]
		if !ok {
			probed, err := engine.CoveredPositions(ctx, t.dir, g.pkgs[0], g.runRegex, coverPkg, opts.OracleTimeout, g.flags, runEnv)
			if err != nil {
				if ctx.Err() != nil {
					return engine.Coverage{}, false, ctx.Err()
				}
				return engine.Coverage{}, false, nil
			}
			got = probed
			cache[key] = got
		}
		coverage = coverage.Merge(got)
	}
	return coverage, true, nil
}

func testNoun(n int) string { return countNoun(n, "test") }

func survivorNoun(n int) string { return countNoun(n, "survivor") }

func killNoun(n int) string { return countNoun(n, "kill") }

// driftRemeasureIndexes deterministically re-identifies a drifted record's
// candidates within the regenerated set and selects the re-measure set: every
// recorded survivor, every kill whose recorded killer is a moved oracle, and
// every timeout or package-scope kill when any oracle moved — those rest on
// the whole recorded set's behavior. Kills keyed to unmoved oracles and all
// discards stand. stand counts the standing kills for the decision line. Any
// re-identification failure refuses the drift serve so the whole target
// re-measures (REQ-result-stale's killer-drift carve-out).
func driftRemeasureIndexes(generation engine.Generation, rec Finding, moved []string) (map[int]bool, int, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, 0, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, 0, false
	}
	movedSet := make(map[string]bool, len(moved))
	for _, symbol := range moved {
		movedSet[symbol] = true
	}
	remeasure := map[int]bool{}
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok {
			return nil, 0, false
		}
		if len(moved) == 0 {
			// No oracle observed the delta, so no oracle's behavior moved:
			// survivals stand exactly like kills and the whole record
			// serves with nothing re-measured.
			continue
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, 0, false
		}
		remeasure[i] = true
	}
	stand := 0
	for _, kill := range rec.Kills {
		i, ok := byIdentity[survivorKey{kill.Position, kill.Operator}]
		if !ok {
			return nil, 0, false
		}
		if remeasure[i] {
			// The identity is already a survivor's: a record naming one
			// candidate both killed and surviving is corrupt (parse refuses
			// persisted documents; an in-memory prior can still carry it).
			return nil, 0, false
		}
		setWide := kill.Killer == TimeoutKiller || strings.HasPrefix(kill.Killer, PackageKillerPrefix)
		if movedSet[kill.Killer] || (setWide && len(moved) != 0) {
			if _, runnable := generation.Candidates[i].Mutant(); !runnable {
				return nil, 0, false
			}
			remeasure[i] = true
			continue
		}
		// No runnable check for a standing kill: it is served, not
		// executed, and deterministic regeneration over the evidence-pinned
		// source reproduces the recorded runnability.
		stand++
	}
	return remeasure, stand, true
}

// grownSurvivorIndexes deterministically re-identifies a grown record's
// candidates and survivors within the regenerated set: the complete count
// and selection length unchanged, every identity unique, and every recorded
// survivor re-identified and still runnable. Any failure refuses the growth
// serve so the whole target re-measures (REQ-result-stale's growth
// carve-out).
func grownSurvivorIndexes(generation engine.Generation, rec Finding) (map[int]bool, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, false
	}
	survivors := make(map[int]bool, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok {
			return nil, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, false
		}
		survivors[i] = true
	}
	return survivors, true
}

// proofAbortError names a canceled campaign's in-flight proof abort:
// the wrap carries the target and its oracle so the failure is
// actionable without re-deriving which subject's view was being built
// (REQ-exec-quiescence's legibility arm), and the join keeps the
// cancellation class inspectable when err is a stored union fault that
// predates the cancellation — the bounded retry is skipped on a
// canceled campaign, so err alone may carry no cancellation.
func proofAbortError(target string, oracle []string, err, ctxErr error) error {
	return fmt.Errorf("freshness proof for target %s (oracle %s): %w", target, strings.Join(oracle, ", "), errors.Join(err, ctxErr))
}

// movedPinAttribution names why a prior finding's pins no longer cover the
// request, via the freshness inspection — the class comes from the
// inspection, never an assumed "stale" (an unverifiable prior is not stale) —
// falling back to the caller's generic note when the inspection cannot
// attribute (REQ-result-stale's decision honesty).
func (t *Tree) movedPinAttribution(ctx context.Context, rec Finding, prebuilt *subjectViewSet, fallback string) string {
	inspection, err := t.inspectFindingStateContext(ctx, rec, prebuilt)
	if err == nil && inspection.State != FindingCurrent && inspection.Reason != "" {
		return string(inspection.State) + ": " + inspection.Reason
	}
	return fallback
}

// applyDisposition folds one disposition into an operator summary: delta +1
// scores a fresh outcome, delta -1 retracts a recorded disposition before its
// replacement is scored. Every counts fold in this file goes through it so
// the disposition vocabulary is written once.
func applyDisposition(summary *OperatorSummary, disposition string, delta int) {
	switch disposition {
	case "killed":
		summary.Killed += delta
	case "survived":
		summary.Survived += delta
	case "discarded":
		summary.Discarded += delta
	}
}

// sumOperatorTotals reconciles a finding's totals from its operator
// summaries (INV-RESULT-CANDIDATE-CONSERVATION's per-operator ↔ totals
// equations have one derivation).
func sumOperatorTotals(operators []OperatorSummary) (killed, discarded, survived int) {
	for _, summary := range operators {
		killed += summary.Killed
		discarded += summary.Discarded
		survived += summary.Survived
	}
	return killed, discarded, survived
}

// candidateIdentityIndex maps every candidate identity to its enumeration
// index, refusing a duplicate identity: deterministic re-identification of
// recorded per-candidate state is only sound when identities are unique
// (REQ-result-stale's carve-outs).
func candidateIdentityIndex(candidates []engine.Candidate) (map[survivorKey]int, bool) {
	byIdentity := make(map[survivorKey]int, len(candidates))
	for i, candidate := range candidates {
		key := survivorKey{candidate.Position, candidate.Operator}
		if _, duplicate := byIdentity[key]; duplicate {
			return nil, false
		}
		byIdentity[key] = i
	}
	return byIdentity, true
}

// countNoun counts grammatically for decision reasons.
func countNoun(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", n, singular)
}

func candidateNoun(n int) string { return countNoun(n, "candidate") }

// flaggedCandidateIndexes deterministically re-identifies a served record's
// flagged candidates and recorded survivors within the regenerated candidate
// set. A record whose identities cannot all be re-identified — a moved
// count, a colliding identity, or a flagged candidate that is no longer
// runnable — cannot be spliced and reports false, sending the whole target
// back to re-measurement (REQ-result-stale).
func flaggedCandidateIndexes(generation engine.Generation, rec Finding) (map[int]bool, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, false
	}
	for _, survivor := range rec.Survivors {
		if _, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]; !ok {
			return nil, false
		}
	}
	flagged := make(map[int]bool, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		i, ok := byIdentity[survivorKey{evidence.Position, evidence.Operator}]
		if !ok {
			return nil, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, false
		}
		flagged[i] = true
	}
	return flagged, true
}

// extendedPrefixStands reports whether a regenerated enumeration under a
// wider budget re-identifies a capped record's measured prefix: the complete
// candidate count is unchanged, the selection is strictly longer than the
// record's prefix, every candidate identity is unique, the recorded
// per-operator selected counts equal the prefix's, and every recorded
// survivor re-identifies inside the prefix. Any failure refuses the extension
// so the whole target re-measures (REQ-result-stale's budget-extension
// carve-out).
func extendedPrefixStands(generation engine.Generation, rec Finding) bool {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) <= rec.Generated {
		return false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return false
	}
	prefixOperators := map[string]int{}
	for _, candidate := range generation.Candidates[:rec.Generated] {
		prefixOperators[candidate.Operator]++
	}
	if len(prefixOperators) != len(rec.Operators) {
		return false
	}
	for _, summary := range rec.Operators {
		if prefixOperators[summary.Operator] != summary.Generated {
			return false
		}
	}
	// Attestations always name survivors (enforced at parse), so the
	// survivor walk covers them too.
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok || i >= rec.Generated {
			return false
		}
	}
	return true
}

// spliceExtendedFinding serves a capped record under a wider budget
// (REQ-mut-budget, REQ-result-stale's budget-extension carve-out): the
// measured prefix keeps its recorded outcomes, dispositions, and attestations
// while each suffix candidate's fresh outcome and evidence are appended,
// conserving candidate accounting over the merged record
// (INV-RESULT-CANDIDATE-CONSERVATION). The suffix processes' completed union
// is reconciled with the record's persisted union folded in: a suffix that
// read only inputs the record already pinned leaves the evidence untouched,
// while a read beyond the record's pins is runtime information it never
// pinned, so the extended outcome is preserved but explicitly non-reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) spliceExtendedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, from int, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string, budget int) (Finding, error) {
	suffix := make(map[int]bool, len(candidates)-from)
	for mi := from; mi < len(candidates); mi++ {
		suffix[mi] = true
	}
	// The recorded union folds into the suffix union before reconciliation,
	// and the extension reports a measurement, never a cached serve.
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, suffix, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, err
	}
	return extendFindingCounts(ctx, spliced.rec, candidates, from, outcomes, killers, spliced.fresh, budget)
}

// splicedEvidence is one splice's evidence outcome: the record after
// fail-closed union reconciliation, the re-executed candidates' fresh
// evidence, the reconciled union (for provenance), and the current attach
// results — kept by the growth arm, whose gate justifies upgrading every
// subject to the current tree's evidence, and discarded by the flagged and
// extension splices, whose cached proof is never upgraded by a partial
// measurement (REQ-exec-observation).
type splicedEvidence struct {
	rec            Finding
	fresh          []CandidateEvidence
	union          runtimeinput.Observation
	targetEvidence SubjectEvidence
	oracleEvidence []SubjectEvidence
}

// spliceRecordedEvidence is the shared evidence spine of every record splice
// (the flagged-candidate re-execution, the budget extension, and the
// oracle-growth serve): union the re-executed processes' observations,
// optionally fold the record's persisted union in first (comparing "recorded
// pins plus fresh reads" against the recorded pins), reconcile against the
// record fail-closed (applySplicedUnion), attach the fresh union so
// post-execution producer validation re-establishes the observation bracket,
// and stamp the current view's compartment ledger — identical to the
// record's whenever the pins matched, and the record's conformance upgrade
// when it predates the ledger (REQ-result-record). Counts folding stays with
// each arm — replacement, append, and survivor-rescore are different truths
// over the same spine.
func (t *Tree) spliceRecordedEvidence(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, reExecuted map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, labels []string, foldRecorded, cached bool) (splicedEvidence, error) {
	union, freshEvidence, err := completedObservationUnion(ctx, t.dir, env, baselines, candidates, outcomes, observations, incompletes, reExecuted)
	if err != nil {
		return splicedEvidence{}, err
	}
	if foldRecorded {
		union, err = t.foldRecordedUnion(ctx, env, rec, targetView.moduleDir, union)
		if err != nil {
			return splicedEvidence{}, err
		}
	}
	union, rec, err = t.applySplicedUnion(ctx, env, rec, union)
	if err != nil {
		return splicedEvidence{}, err
	}
	targetEvidence, oracleEvidence, err := attachEvidence(targetView, oracleViews, union)
	if err != nil {
		return splicedEvidence{}, err
	}
	rec.CompartmentLedger = compartmentLedgerFromView(currentLedger)
	rec.Labels = append([]string(nil), labels...)
	rec.Cached = cached
	return splicedEvidence{rec: rec, fresh: freshEvidence, union: union, targetEvidence: targetEvidence, oracleEvidence: oracleEvidence}, nil
}

// foldRecordedUnion merges the served record's persisted completed-process
// union into the suffix processes' fresh union, so the extension's
// reconciliation compares "recorded pins plus suffix reads" against the
// recorded pins alone: a suffix that read only inputs the record already
// pinned folds to exactly the persisted union, while a read beyond the
// record's pins survives the fold and diverges. Adoption re-evaluates the
// persisted manifest against the current tree; a moved or unadoptable
// manifest is left out, and the resulting divergence stamps the extended
// finding non-reusable rather than serving unpinned runtime information
// (REQ-result-stale's fail-closed bound).
func (t *Tree) foldRecordedUnion(ctx context.Context, env []string, rec Finding, moduleDir string, union runtimeinput.Observation) (runtimeinput.Observation, error) {
	adopted, adoptErr := runtimeinput.AdoptEnv(rec.TargetEvidence.RuntimeInputs, moduleDir, fmt.Sprintf("gomutant-extend-%d", findingObservationSequence.Add(1)), env)
	if adoptErr != nil {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		return union, nil
	}
	return mergeFindingObservationsContext(ctx, t.dir, env, union, adopted)
}

// extendFindingCounts appends each suffix candidate's fresh outcome to the
// record — per operator and in the finding totals — while every prefix
// candidate keeps its recorded disposition, survivor identity, and
// attestation (INV-RESULT-CANDIDATE-CONSERVATION; the prefix pins did not
// move, so its attestations ride unchanged per REQ-attest-survivor). Suffix
// survivors are appended in candidate order, the suffix run's candidate
// evidence becomes the record's (an extendable record carries none), and the
// budget, generated, and candidate counts record the merged truth.
func extendFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, from int, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence, budget int) (Finding, error) {
	// Prefix kill attributions carry verbatim — their pins did not move —
	// and suffix kills append theirs; a record predating attribution stays
	// without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	kills := append([]Kill(nil), rec.Kills...)
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	survivors := append([]Survivor(nil), rec.Survivors...)
	// Suffix survivors of a divergence-stamped extension classify
	// unstable-oracle at once — their measurement is what ran under unstable
	// runtime conditions — while carried prefix survivors keep their
	// recorded buckets verbatim; a verifiable extension's suffix survivors
	// stay unbucketed here and earn fresh probes afterwards
	// (REQ-exec-survivor-evidence; advisory, never a pin).
	suffixExecution := ""
	if rec.TargetEvidence.RuntimeUnverifiable {
		suffixExecution = "unstable-oracle"
	}
	for mi := from; mi < len(candidates); mi++ {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		candidate := candidates[mi]
		i, ok := byOperator[candidate.Operator]
		if !ok {
			i = len(operators)
			operators = append(operators, OperatorSummary{Operator: candidate.Operator})
			byOperator[candidate.Operator] = i
		}
		operators[i].Generated++
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: suffixExecution})
		case "killed":
			if killsComplete {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
			}
		}
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].Operator < operators[j].Operator })
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = freshEvidence
	rec.Budget = budget
	rec.Generated = len(candidates)
	return rec, nil
}

// spliceServedFinding serves a record whose only unverifiable runtime evidence
// is candidate-local: covered candidates keep their recorded outcomes while
// each flagged candidate's fresh outcome and evidence replace its recorded
// ones, conserving per-operator and total candidate accounting
// (REQ-result-stale, INV-RESULT-CANDIDATE-CONSERVATION). The fresh completed
// union must agree with the record's completed-process union so the spliced
// evidence covers the re-executed processes without shedding any served
// process's pinned runtime inputs; fresh observations that diverge are runtime
// information the record never pinned, so the spliced outcome is preserved but
// explicitly non-reusable (REQ-exec-observation).
func (t *Tree) spliceServedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, flagged map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string) (Finding, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, flagged, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, false, true)
	if err != nil {
		return Finding{}, err
	}
	return spliceFindingCounts(ctx, spliced.rec, candidates, flagged, outcomes, killers, spliced.fresh)
}

// applySplicedUnion reconciles the re-executed processes' completed union with
// the served record's persisted union. An equal union leaves the record's
// evidence untouched; a diverged one — different manifest, different digest,
// or an unverifiable fresh state — folds an explicit incomplete observation
// into the union and stamps every subject's evidence with the resulting
// unverifiable state, so the spliced finding is preserved but never reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) applySplicedUnion(ctx context.Context, env []string, rec Finding, union runtimeinput.Observation) (runtimeinput.Observation, Finding, error) {
	state, err := runtimeinput.CompletedState(union)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, err
	}
	if !splicedUnionDiverged(state, rec.TargetEvidence) {
		return union, rec, nil
	}
	if !state.Unverifiable {
		incomplete, incompleteErr := runtimeinput.IncompleteEnv(t.dir, fmt.Sprintf("gomutant-splice-%d", findingObservationSequence.Add(1)), "runtime input observations diverged from the served record's completed-process union", env)
		if incompleteErr != nil {
			return runtimeinput.Observation{}, Finding{}, incompleteErr
		}
		if union, err = mergeFindingObservationsContext(ctx, t.dir, env, union, incomplete); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
		if state, err = runtimeinput.CompletedState(union); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
	}
	rec.TargetEvidence = withRuntimeState(rec.TargetEvidence, state)
	for i := range rec.OracleEvidence {
		rec.OracleEvidence[i] = withRuntimeState(rec.OracleEvidence[i], state)
	}
	return union, rec, nil
}

// splicedUnionDiverged reports whether the re-executed processes' completed
// union no longer equals the served record's persisted union. A diverged
// union makes the spliced finding explicitly non-reusable (REQ-result-stale):
// keeping it reusable would serve kills whose processes read inputs the
// record never pinned — the forbidden flattering direction.
func splicedUnionDiverged(state runtimeinput.State, prior SubjectEvidence) bool {
	return state.Unverifiable || state.Manifest != prior.RuntimeInputs || state.Digest != prior.RuntimeDigest
}

// spliceFindingCounts replaces each flagged candidate's recorded disposition
// with its fresh outcome — per operator and in the finding totals — while
// every covered candidate keeps its recorded one
// (INV-RESULT-CANDIDATE-CONSERVATION). Survivor identities are rebuilt in
// candidate order carrying their recorded advisory buckets — exact under the
// pins the serve verified — an attestation rides only a survivor that
// survives again at the same position and operator (REQ-attest-survivor),
// and the fresh candidate evidence replaces the served record's.
func spliceFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, flagged map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence) (Finding, error) {
	// Covered kills carry their recorded attribution, flagged candidates
	// that die again record their fresh killer; a record predating
	// attribution stays without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	recordedKills := make(map[survivorKey]string, len(rec.Kills))
	for _, kill := range rec.Kills {
		recordedKills[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	var kills []Kill
	operators := append([]OperatorSummary(nil), rec.Operators...)
	rec.Operators = operators
	byOperator := make(map[string]*OperatorSummary, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = &operators[i]
	}
	priorSurvivors := make(map[survivorKey]Survivor, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorSurvivors[survivorKey{survivor.Position, survivor.Operator}] = survivor
	}
	priorEvidence := make(map[survivorKey]CandidateEvidence, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		priorEvidence[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	freshByKey := make(map[survivorKey]CandidateEvidence, len(freshEvidence))
	for _, evidence := range freshEvidence {
		freshByKey[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	var survivors []Survivor
	var candidateEvidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		key := survivorKey{candidate.Position, candidate.Operator}
		if !flagged[mi] {
			if prior, ok := priorSurvivors[key]; ok {
				// A covered survivor carries its recorded advisory bucket
				// verbatim, like its disposition and attestation
				// (REQ-exec-survivor-evidence).
				survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: prior.Execution})
			}
			if killer, ok := recordedKills[key]; ok {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killer})
			}
			continue
		}
		summary := byOperator[candidate.Operator]
		if summary == nil {
			return Finding{}, fmt.Errorf("gomutant: spliced candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		applyDisposition(summary, priorEvidence[key].Disposition, -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(summary, disposition, 1)
		if disposition == "survived" {
			// A re-executed candidate surviving again keeps its recorded
			// bucket when it has one: the pins the serve verified are
			// exactly what makes the recorded classification still exact.
			// A flip into survival (recorded killed or discarded) has no
			// recorded bucket and stays unbucketed like a probe-refused
			// survivor. Under a divergence-stamped splice the re-executed
			// survivor classifies unstable-oracle instead — its
			// re-execution is what ran under unstable runtime conditions —
			// while covered survivors above keep their recorded buckets
			// verbatim (REQ-exec-survivor-evidence).
			execution := priorSurvivors[key].Execution
			if rec.TargetEvidence.RuntimeUnverifiable {
				execution = "unstable-oracle"
			}
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Execution: execution})
		}
		if disposition == "killed" && killsComplete {
			kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
		}
		if entry, ok := freshByKey[key]; ok {
			candidateEvidence = append(candidateEvidence, entry)
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, fmt.Errorf("gomutant: spliced operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = candidateEvidence
	// A disposition rides only a survivor that survives again at the same
	// position and operator (REQ-attest-survivor).
	current := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		current[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var attested []Attestation
	for _, attestation := range rec.Attested {
		if current[survivorKey{attestation.Position, attestation.Operator}] {
			attested = append(attested, attestation)
		}
	}
	rec.Attested = attested
	return rec, nil
}

func withRuntimeState(evidence SubjectEvidence, state runtimeinput.State) SubjectEvidence {
	evidence.RuntimeInputs = state.Manifest
	evidence.RuntimeDigest = state.Digest
	evidence.RuntimeUnverifiable = state.Unverifiable
	evidence.RuntimeReason = state.Reason
	return evidence
}

func summarizeOperators(candidates []engine.Candidate, outcomes []engine.MutantOutcome) []OperatorSummary {
	byOperator := map[string]*OperatorSummary{}
	operators := make([]string, 0)
	for i, candidate := range candidates {
		summary := byOperator[candidate.Operator]
		if summary == nil {
			summary = &OperatorSummary{Operator: candidate.Operator}
			byOperator[candidate.Operator] = summary
			operators = append(operators, candidate.Operator)
		}
		summary.Generated++
		applyDisposition(summary, outcomeDisposition(outcomes[i]), 1)
	}
	sort.Strings(operators)
	summaries := make([]OperatorSummary, 0, len(operators))
	for _, operator := range operators {
		summaries = append(summaries, *byOperator[operator])
	}
	return summaries
}

// attributedKill enforces the oracle as the sole arbiter (REQ-target-oracle,
// REQ-exec-attribution): a kill must name an oracle test, a timeout, or a
// probe-confirmed package failure. A named killer outside the oracle means
// the run pattern matched a test the target never claimed — an
// unattributable measurement, aborted rather than scored.
func attributedKill(killer string, oracleSet map[string]bool) error {
	if killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) {
		return nil
	}
	if oracleSet[killer] {
		return nil
	}
	return fmt.Errorf("killed by %s, which is not in the target's oracle", killer)
}

// TimeoutKiller and PackageKillerPrefix re-export the engine's kill
// attributions for callers reading finding output.
const (
	TimeoutKiller       = engine.TimeoutKiller
	PackageKillerPrefix = engine.PackageKillerPrefix
)

// windowKill names one confirmable kill: the window-local target index
// and the candidate index within it.
type windowKill struct{ target, mi int }

// confirmWindowKills walks every confirmable kill of one execution
// window — targets in order, candidates in order within each — under
// per-target stride gates sharing the window's flip signal, because the
// pool load the gate samples against was shared by every target of the
// window (REQ-exec-attribution's stride-gate clause). Evidence flows
// where it binds: a flip anywhere marks every gate flipped and confirms
// every stride-skipped kill of the whole window; serial evidence
// arriving unverifiable re-arms that target's gate volatile and
// confirms its own skips (they were sampled under a clean assumption
// the serial run just withdrew); drained kills feed the same evidence
// path, so a collision observed during a drain still un-samples the
// window. Deterministic by construction: candidate order in, explicit
// FIFO processing, no worker-timing input.
func confirmWindowKills(volatile func(target int) bool, kills []windowKill, confirm func(windowKill) (confirmOutcome, error), unverifiable func(windowKill) bool) error {
	gates := map[int]*confirmationGate{}
	windowFlipped := false
	var skipped []windowKill
	gateFor := func(target int) *confirmationGate {
		g := gates[target]
		if g == nil {
			g = &confirmationGate{volatile: volatile(target), flipped: windowFlipped}
			gates[target] = g
		}
		return g
	}
	takeSkips := func(target int, all bool) []windowKill {
		var taken, kept []windowKill
		for _, k := range skipped {
			if all || k.target == target {
				taken = append(taken, k)
			} else {
				kept = append(kept, k)
			}
		}
		skipped = kept
		return taken
	}
	for _, k := range kills {
		if !gateFor(k.target).confirmNow() {
			skipped = append(skipped, k)
			continue
		}
		queue := []windowKill{k}
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			outcome, err := confirm(next)
			if err != nil {
				return err
			}
			g := gateFor(next.target)
			if !g.volatile && unverifiable(next) {
				g.volatile = true
				queue = append(queue, takeSkips(next.target, false)...)
			}
			if g.observe(outcome) && !windowFlipped {
				// Kills walk in target order, so already-walked gates
				// are never consulted again: seeding windowFlipped into
				// every later-constructed gate is the whole sibling
				// propagation.
				windowFlipped = true
				queue = append(queue, takeSkips(0, true)...)
			}
		}
	}
	return nil
}

// classifyConfirmation maps one serial confirmation's scored result to
// gate evidence: a serial timeout is excluded from confirmation in
// both directions (no evidence either way), a reproduced kill extends
// the streak, anything else is a demonstrated collision.
func classifyConfirmation(outcome engine.MutantOutcome, killer string) confirmOutcome {
	switch {
	case outcome == engine.MutantKilled && killer == engine.TimeoutKiller:
		return confirmInconclusive
	case outcome == engine.MutantKilled:
		return confirmReproduced
	case outcome == engine.MutantDiscarded:
		// A discard proves nothing either way by its own clauses —
		// noise and probe-timeout discards are not demonstrated
		// collisions.
		return confirmInconclusive
	}
	return confirmFlipped
}

// windowEvidenceVolatile reports whether a target's window carries any
// unverifiable runtime evidence — in its baselines or any completed
// mutant observation — the signature of load-sensitive inputs the
// stride gate must not sample across (REQ-exec-attribution).
func windowEvidenceVolatile(w *work, observations []runtimeinput.Observation) bool {
	for _, b := range w.baselines {
		if b.Unverifiable {
			return true
		}
	}
	for _, o := range observations {
		if o.Unverifiable {
			return true
		}
	}
	return false
}
