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

	derivedOracles map[string][]string
	validations    map[string]oracleValidationResult
	contexts       map[string]packageContextResult
	rapid          map[string]bool
}

func newRunPreparation(t *Tree) *runPreparation {
	return &runPreparation{
		packageOf:      t.eng.PackageOfContext,
		testsOf:        t.eng.TestsOfContext,
		validate:       t.eng.ValidateOracleContext,
		contextFor:     t.eng.PackageContextContext,
		splitRapidPkgs: t.eng.SplitRapidPkgsContext,
		derivedOracles: map[string][]string{},
		validations:    map[string]oracleValidationResult{},
		contexts:       map[string]packageContextResult{},
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
	baselines   []runtimeinput.Observation
	// serve is the prior record being served with candidate-local
	// re-execution (REQ-result-stale): only the candidate indexes in
	// flagged execute, and phase three splices their fresh outcomes and
	// evidence into the served record.
	serve   *Finding
	flagged map[int]bool
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
		out, groupKiller, state, incomplete, err := engine.RunMutantObservedEnv(ctx, t.dir, m, g.pkgs, g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, runEnv)
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
// never a measurement pin.
func (t *Tree) bucketSurvivorExecution(ctx context.Context, f *Finding, w work, opts Options, runEnv []string, cache map[string]engine.Coverage) error {
	if len(f.Survivors) == 0 {
		return nil
	}
	if f.TargetEvidence.RuntimeUnverifiable {
		for si := range f.Survivors {
			f.Survivors[si].Execution = "unstable-oracle"
		}
		return nil
	}
	coverPkg := w.targetView.subject.Package
	coverage := engine.Coverage{}
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg
		got, ok := cache[key]
		if !ok {
			probed, err := engine.CoveredPositions(ctx, t.dir, g.pkgs[0], g.runRegex, coverPkg, opts.OracleTimeout, g.flags, runEnv)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Best-effort: an unprobeable oracle leaves the bucket
				// empty rather than failing a run whose measurement is
				// already sound.
				return nil
			}
			got = probed
			cache[key] = got
		}
		coverage = coverage.Merge(got)
	}
	for si, survivor := range f.Survivors {
		file, line, col, ok := splitSurvivorPosition(survivor.Position)
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
		if hasPrior && !opts.Force && budgetCovers(*rec, opts.Budget) {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String())
			if err != nil {
				return nil, err
			}
			if !matches {
				// The moved pin is named so a caller who just wrote
				// kill-tests sees the tool noticing them instead of
				// forcing defensively (REQ-result-stale). The class comes
				// from the inspection, not an assumed "stale": an
				// unverifiable prior is not stale.
				inspection, ierr := t.inspectFindingStateContext(ctx, *rec)
				switch {
				case ierr == nil && inspection.State != FindingCurrent && inspection.Reason != "":
					reason = string(inspection.State) + ": " + inspection.Reason
				default:
					reason = "stale: a measurement pin moved (oracle timeout, oracle selection, operator set, or runtime inputs moved during evaluation)"
				}
			}
			if matches && len(rec.CandidateEvidence) == 0 {
				cached := *rec
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
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

		targetView.module.producer = true
		for _, oracleView := range oracleViews {
			oracleView.module.producer = true
		}
		producerViews, err := t.newSubjectViewsWithPackageContext(ctx, append([]string{tg.Symbol}, oracle...), preparation.packageContext, true, engines)
		if err != nil {
			// The wrap names the target and its oracle so a freshness
			// failure is actionable without re-deriving which subject the
			// view was built for (REQ-exec-quiescence's legibility arm).
			return nil, fmt.Errorf("freshness proof for target %s (oracle %s): %w", tg.Symbol, strings.Join(oracle, ", "), err)
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
		pending = append(pending, work{target: i, oracle: oracle, reason: reason, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, producer: producerViews, serve: serve})
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
		generation, err := t.eng.CandidatesContext(ctx, tg.Symbol, budget)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		if w.serve != nil {
			if flagged, ok := flaggedCandidateIndexes(generation, *w.serve); ok {
				w.candidates = generation.Candidates
				w.flagged = flagged
				decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: fmt.Sprintf("served: pins unchanged; %d candidate(s) re-execute", len(flagged)), Candidates: len(flagged)}
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
		if w.serve == nil {
			w.candidates = generation.Candidates
			decisions[w.target] = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: w.reason, Candidates: len(generation.Candidates)}
			f.Budget = opts.Budget
			f.CandidateCount = generation.CandidateCount
			f.Generated = len(generation.Candidates)
		}
		// Per-package oracle scoping (REQ-exec-oracle-run), with the rapid
		// failfile flag only in front of binaries that register it
		// (REQ-mut-overlay).
		runs := pkgRuns(w.oracle)
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
	type job struct{ wi, mi int }
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
				observations[j.wi][j.mi] = state
				incompletes[j.wi][j.mi] = incompleteReason
				killers[j.wi][j.mi] = killer
				outcomes[j.wi][j.mi] = outcome
			}
		}()
	}
dispatching:
	for wi := range pending {
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
	if jobs > 1 {
		for wi := range pending {
			for mi := range pending[wi].candidates {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if outcomes[wi][mi] != engine.MutantKilled || killers[wi][mi] == engine.TimeoutKiller {
					continue
				}
				m, runnable := pending[wi].candidates[mi].Mutant()
				if !runnable {
					continue
				}
				outcome, killer, state, incomplete, err := t.executeMutant(ctx, pending[wi], m, opts, runEnv)
				if err != nil {
					return nil, err
				}
				outcomes[wi][mi] = outcome
				killers[wi][mi] = killer
				observations[wi][mi] = state
				incompletes[wi][mi] = incomplete
			}
		}
	}
	if opts.afterExecution != nil {
		opts.afterExecution()
	}
	// A drifted producer view refuses target-locally, not campaign-wide:
	// the per-target validations below decide which findings still bind,
	// so a concurrent edit costs only the affected targets
	// (REQ-exec-quiescence).
	var treeDrift error
	var drifted []TargetDrift
	if err := views.validateProducers(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		treeDrift = err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase three, sequential: aggregate in target and mutant order.
	for wi, w := range pending {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.aggregate != nil {
			opts.aggregate()
		}
		f := &findings[w.target]
		if w.serve != nil {
			spliced, err := t.spliceServedFinding(ctx, runEnv, *w.serve, w.candidates, w.flagged, w.baselines, w.targetView, w.oracleViews, outcomes[wi], observations[wi], incompletes[wi], targets[w.target].Labels)
			if err != nil {
				return nil, err
			}
			if err := w.producer.validateProducers(ctx); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
				continue
			}
			findings[w.target] = spliced
			if err := commitFinding(ctx, repository, opts.Commit, spliced); err != nil {
				return nil, err
			}
			continue
		}
		state, candidateEvidence, err := completedObservationUnion(ctx, t.dir, runEnv, w.baselines, w.candidates, outcomes[wi], observations[wi], incompletes[wi], nil)
		if err != nil {
			return nil, err
		}
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
		if err := w.producer.validateProducers(ctx); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			drifted = append(drifted, TargetDrift{Symbol: targets[w.target].Symbol, Reason: err.Error()})
			continue
		}
		f.Commit = repository.commit
		sourceFiles := append([]string(nil), w.targetView.sourceFiles...)
		for _, oracleView := range w.oracleViews {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sourceFiles = append(sourceFiles, oracleView.sourceFiles...)
		}
		historical, err := repository.historicalPackageFilesContext(ctx, sourceFiles)
		if err != nil {
			return nil, err
		}
		sourceFiles = append(sourceFiles, historical...)
		sourceFiles = withModuleSelectionPaths(sourceFiles)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceFiles = append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum"))
		f.Dirty, err = repository.pathsDirtyContext(ctx, sourceFiles, state.State)
		if err != nil {
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
			}
		}
		if err := t.bucketSurvivorExecution(ctx, f, w, opts, runEnv, coverageCache); err != nil {
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
		if err := commitFinding(ctx, repository, opts.Commit, *f); err != nil {
			return nil, err
		}
		if opts.Guidance != nil && f.TargetEvidence.RuntimeUnverifiable && !f.OracleExplicit {
			// Targets sharing one oracle set share one attribution: the
			// probes run once per set, not per finding.
			key := strings.Join(slices.Sorted(slices.Values(w.oracle)), "\x00")
			attr, ok := guidanceCache[key]
			if !ok {
				probed, gerr := t.probeOracleInstability(ctx, w.oracle, w.groups, opts, runEnv)
				if gerr != nil {
					return nil, gerr
				}
				attr = probed
				guidanceCache[key] = attr
			}
			opts.Guidance(buildOracleGuidance(targets[w.target].Symbol, f.TargetEvidence.RuntimeReason, w.oracle, attr))
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
		snapshot[i].Labels = slices.Clone(snapshot[i].Labels)
		snapshot[i].OracleEvidence = slices.Clone(snapshot[i].OracleEvidence)
		snapshot[i].Operators = slices.Clone(snapshot[i].Operators)
		snapshot[i].Survivors = slices.Clone(snapshot[i].Survivors)
		snapshot[i].Attested = slices.Clone(snapshot[i].Attested)
		snapshot[i].CandidateEvidence = slices.Clone(snapshot[i].CandidateEvidence)
	}
	return snapshot
}

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
	byIdentity := make(map[survivorKey]int, len(generation.Candidates))
	for i, candidate := range generation.Candidates {
		key := survivorKey{candidate.Position, candidate.Operator}
		if _, duplicate := byIdentity[key]; duplicate {
			return nil, false
		}
		byIdentity[key] = i
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
func (t *Tree) spliceServedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, flagged map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, labels []string) (Finding, error) {
	union, freshEvidence, err := completedObservationUnion(ctx, t.dir, env, baselines, candidates, outcomes, observations, incompletes, flagged)
	if err != nil {
		return Finding{}, err
	}
	union, rec, err = t.applySplicedUnion(ctx, env, rec, union)
	if err != nil {
		return Finding{}, err
	}
	// Attach the fresh union so post-execution producer validation
	// re-establishes the observation bracket around the re-executed
	// processes. The persisted subject evidence stays the served record's:
	// cached proof is never upgraded by a partial re-execution
	// (REQ-exec-observation).
	if _, _, err := attachEvidence(targetView, oracleViews, union); err != nil {
		return Finding{}, err
	}
	rec.Labels = append([]string(nil), labels...)
	rec.Cached = true
	return spliceFindingCounts(ctx, rec, candidates, flagged, outcomes, freshEvidence)
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
// candidate order, an attestation rides only a survivor that survives again
// at the same position and operator (REQ-attest-survivor), and the fresh
// candidate evidence replaces the served record's.
func spliceFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, flagged map[int]bool, outcomes []engine.MutantOutcome, freshEvidence []CandidateEvidence) (Finding, error) {
	operators := append([]OperatorSummary(nil), rec.Operators...)
	rec.Operators = operators
	byOperator := make(map[string]*OperatorSummary, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = &operators[i]
	}
	priorSurvivors := make(map[survivorKey]bool, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorSurvivors[survivorKey{survivor.Position, survivor.Operator}] = true
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
			if priorSurvivors[key] {
				survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator})
			}
			continue
		}
		summary := byOperator[candidate.Operator]
		if summary == nil {
			return Finding{}, fmt.Errorf("gomutant: spliced candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		switch priorEvidence[key].Disposition {
		case "killed":
			summary.Killed--
		case "survived":
			summary.Survived--
		case "discarded":
			summary.Discarded--
		}
		switch outcomeDisposition(outcomes[mi]) {
		case "killed":
			summary.Killed++
		case "survived":
			summary.Survived++
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator})
		case "discarded":
			summary.Discarded++
		}
		if entry, ok := freshByKey[key]; ok {
			candidateEvidence = append(candidateEvidence, entry)
		}
	}
	killed, discarded, survived := 0, 0, 0
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, fmt.Errorf("gomutant: spliced operator %s counts do not reconcile", summary.Operator)
		}
		killed += summary.Killed
		discarded += summary.Discarded
		survived += summary.Survived
	}
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
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
		switch outcomes[i] {
		case engine.MutantDiscarded:
			summary.Discarded++
		case engine.MutantKilled:
			summary.Killed++
		case engine.MutantSurvived:
			summary.Survived++
		}
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
