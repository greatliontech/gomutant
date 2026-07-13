package gomutant

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// Options bound a run.
type Options struct {
	// Budget caps mutants per symbol; 0 means all (REQ-mut-budget).
	Budget int
	// Timeout bounds one mutant's oracle run; 0 means 60s.
	Timeout time.Duration
	// Force re-measures targets whose prior finding's pins still match.
	Force bool
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
	Symbol  string `json:"symbol"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
	Mutants int    `json:"mutants,omitempty"`
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
		summary.Generated += finding.Mutants + finding.Discarded
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
	packageOf      func(string) (string, string)
	testsOf        func(string) []string
	validate       func([]string) error
	contextFor     func(string) (string, string, error)
	splitRapidPkgs func([]string) ([]string, []string)

	derivedOracles map[string][]string
	validations    map[string]oracleValidationResult
	contexts       map[string]packageContextResult
	rapid          map[string]bool
}

func newRunPreparation(t *Tree) *runPreparation {
	return &runPreparation{
		packageOf:      t.eng.PackageOf,
		testsOf:        t.eng.TestsOf,
		validate:       t.eng.ValidateOracle,
		contextFor:     t.eng.PackageContext,
		splitRapidPkgs: t.eng.SplitRapidPkgs,
		derivedOracles: map[string][]string{},
		validations:    map[string]oracleValidationResult{},
		contexts:       map[string]packageContextResult{},
	}
}

func (p *runPreparation) oracle(target Target) []string {
	if len(target.Oracle) > 0 || target.OracleExplicit {
		return slices.Clone(target.Oracle)
	}
	pkg, _ := p.packageOf(target.Symbol)
	if pkg == "" {
		return nil
	}
	if oracle, ok := p.derivedOracles[pkg]; ok {
		return slices.Clone(oracle)
	}
	oracle := p.testsOf(pkg)
	p.derivedOracles[pkg] = slices.Clone(oracle)
	return oracle
}

func (p *runPreparation) validateOracle(oracle []string) error {
	key := sequenceKey(oracle)
	if result, ok := p.validations[key]; ok {
		return result.err
	}
	err := p.validate(oracle)
	p.validations[key] = oracleValidationResult{err: err}
	return err
}

func (p *runPreparation) packageContext(pkg string) (string, string, error) {
	if result, ok := p.contexts[pkg]; ok {
		return result.moduleDir, result.packageDir, result.err
	}
	moduleDir, packageDir, err := p.contextFor(pkg)
	p.contexts[pkg] = packageContextResult{moduleDir: moduleDir, packageDir: packageDir, err: err}
	return moduleDir, packageDir, err
}

func (p *runPreparation) rapidPackages(candidates []string) map[string]bool {
	if p.rapid != nil {
		return p.rapid
	}
	p.rapid = map[string]bool{}
	rapid, _ := p.splitRapidPkgs(candidates)
	for _, pkg := range rapid {
		p.rapid[pkg] = true
	}
	return p.rapid
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
// timeout, and budget pin holds, unless forced (REQ-result-stale). A run that
// cannot attribute an outcome aborts without findings
// (REQ-core-attributed-kills).
func (t *Tree) Run(ctx context.Context, targets []Target, opts Options) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Budget < 0 {
		return nil, fmt.Errorf("gomutant: budget must be non-negative")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	targets = snapshotTargets(targets)
	opts.Prior = snapshotFindings(opts.Prior)
	repository := captureRepositoryState(t.dir)
	runEnv := t.eng.GoEnv()
	preparation := newRunPreparation(t)
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
	type work struct {
		target      int
		mutants     []engine.Mutant
		groups      []group
		oracleSet   map[string]bool
		targetView  *subjectView
		oracleViews []*subjectView
		baselines   []runtimeinput.State
	}
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
	baselineCache := map[baselineKey]runtimeinput.State{}
	decisions := make([]RunDecision, len(targets))
	for i, tg := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationResolving, Symbol: tg.Symbol})
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Labels: tg.Labels, OperatorSet: engine.OperatorSet, OracleExplicit: tg.OracleExplicit || len(tg.Oracle) != 0, Timeout: opts.Timeout.String()}
		oracle := preparation.oracle(tg)
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default).
			f.Skipped = "no oracle"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		if err := preparation.validateOracle(oracle); err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		bodyHash, err := t.eng.BodyHash(tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			f.Skipped = "not a function"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		f.BodyHash = bodyHash
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationFreshness, Symbol: tg.Symbol})
		resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle})
		subjectSymbols = append(subjectSymbols, tg.Symbol)
		subjectSymbols = append(subjectSymbols, oracle...)
	}
	views := &subjectViewSet{bySymbol: map[string]*subjectView{}}
	if len(subjectSymbols) != 0 {
		var err error
		views, err = t.newSubjectViewsWithPackageContext(ctx, subjectSymbols, preparation.packageContext)
		if err != nil {
			return nil, fmt.Errorf("freshness: %w", err)
		}
	}
	var oraclePackages []string
	seenOraclePackage := map[string]bool{}
	for _, resolved := range resolvedTargets {
		for _, run := range pkgRuns(resolved.oracle) {
			if !seenOraclePackage[run.pkg] {
				seenOraclePackage[run.pkg] = true
				oraclePackages = append(oraclePackages, run.pkg)
			}
		}
	}
	for _, resolved := range resolvedTargets {
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
			case !budgetCovers(rec.Budget, opts.Budget):
				reason = "budget"
			default:
				reason = "stale"
			}
		}
		if hasPrior && !opts.Force && budgetCovers(rec.Budget, opts.Budget) {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.Timeout.String())
			if err != nil {
				return nil, err
			}
			if matches {
				cached := *rec
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
				findings[i] = cached
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "cached"}
				continue
			}
		}

		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
		mutants, err := t.eng.Mutants(tg.Symbol, opts.Budget)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason, Mutants: len(mutants)}
		f.Budget = opts.Budget
		if opts.Budget > 0 && len(mutants) < opts.Budget {
			// The cap did not bind: the run is exhaustive, and the finding
			// should answer exhaustive requests from cache.
			f.Budget = 0
		}
		// Per-package oracle scoping (REQ-exec-oracle-run), with the rapid
		// failfile flag only in front of binaries that register it
		// (REQ-mut-overlay).
		runs := pkgRuns(oracle)
		pkgs := make([]string, 0, len(runs))
		for _, pr := range runs {
			pkgs = append(pkgs, pr.pkg)
		}
		rapid := preparation.rapidPackages(oraclePackages)
		var groups []group
		for _, pr := range runs {
			var flags []string
			if rapid[pr.pkg] {
				flags = []string{"-rapid.nofailfile"}
			}
			moduleDir, packageDir, err := preparation.packageContext(pr.pkg)
			if err != nil {
				return nil, err
			}
			groups = append(groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		baselines := make([]runtimeinput.State, 0, len(groups))
		for _, group := range groups {
			key := baselineKey{pkg: group.pkgs[0], run: group.runRegex, flags: strings.Join(group.flags, "\x00"), moduleDir: group.moduleDir, packageDir: group.packageDir}
			state, ok := baselineCache[key]
			if !ok {
				reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0]})
				ran, passed, observed, err := engine.TestProbeObservedEnv(ctx, t.dir, group.pkgs[0], group.runRegex, opts.Timeout, group.flags, group.moduleDir, group.packageDir, runEnv)
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
				baselineCache[key] = state
			}
			baselines = append(baselines, state)
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		targetView.module.producer = true
		for _, oracleView := range oracleViews {
			oracleView.module.producer = true
		}
		pending = append(pending, work{target: i, mutants: mutants, groups: groups, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, baselines: baselines})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Decision != nil {
		for _, decision := range decisions {
			opts.Decision(decision)
		}
	}

	// Phase two: the pool. Outcomes land in a preallocated matrix so
	// aggregation is deterministic regardless of completion order; the first
	// error cancels everything in flight.
	outcomes := make([][]engine.MutantOutcome, len(pending))
	observations := make([][]runtimeinput.State, len(pending))
	for wi := range pending {
		outcomes[wi] = make([]engine.MutantOutcome, len(pending[wi].mutants))
		observations[wi] = make([]runtimeinput.State, len(pending[wi].mutants))
	}
	type job struct{ wi, mi int }
	jobCh := make(chan job)
	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var errOnce sync.Once
	var poolErr error
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				w := pending[j.wi]
				m := w.mutants[j.mi]
				outcome := engine.MutantSurvived
				var processStates []runtimeinput.State
				for _, g := range w.groups {
					if outcome != engine.MutantSurvived {
						break
					}
					out, killer, state, err := engine.RunMutantObservedEnv(poolCtx, t.dir, m, g.pkgs, g.runRegex, opts.Timeout, g.flags, g.moduleDir, g.packageDir, runEnv)
					processStates = append(processStates, state)
					if err == nil && out == engine.MutantKilled {
						err = attributedKill(killer, w.oracleSet)
					}
					if err != nil {
						errOnce.Do(func() {
							poolErr = fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
							cancel()
						})
						return
					}
					outcome = out
				}
				state, err := mergeFindingObservations(t.dir, runEnv, processStates...)
				if err != nil {
					errOnce.Do(func() {
						poolErr = fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
						cancel()
					})
					return
				}
				observations[j.wi][j.mi] = state
				outcomes[j.wi][j.mi] = outcome
			}
		}()
	}
	for wi := range pending {
		for mi := range pending[wi].mutants {
			select {
			case jobCh <- job{wi, mi}:
			case <-poolCtx.Done():
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
	if err := views.validateProducers(ctx); err != nil {
		return nil, fmt.Errorf("validate freshness: %w", err)
	}

	// Phase three, sequential: aggregate in target and mutant order.
	for wi, w := range pending {
		f := &findings[w.target]
		states := append([]runtimeinput.State(nil), w.baselines...)
		states = append(states, observations[wi]...)
		state, err := mergeFindingObservations(t.dir, runEnv, states...)
		if err != nil {
			return nil, err
		}
		targetEvidence, oracleEvidence, err := attachEvidence(w.targetView, w.oracleViews, state)
		if err != nil {
			return nil, err
		}
		f.TargetEvidence = targetEvidence
		f.OracleEvidence = oracleEvidence
		f.Commit = repository.commit
		sourceFiles := append([]string(nil), w.targetView.sourceFiles...)
		for _, oracleView := range w.oracleViews {
			sourceFiles = append(sourceFiles, oracleView.sourceFiles...)
		}
		sourceFiles = append(sourceFiles, repository.historicalPackageFiles(sourceFiles)...)
		sourceFiles = withModuleSelectionPaths(sourceFiles)
		sourceFiles = append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum"))
		f.Dirty = repository.pathsDirty(sourceFiles, state)
		f.Operators = summarizeOperators(w.mutants, outcomes[wi])
		for _, summary := range f.Operators {
			f.Discarded += summary.Discarded
			f.Mutants += summary.Killed + summary.Survived
			f.Killed += summary.Killed
		}
		for mi, m := range w.mutants {
			switch outcomes[wi][mi] {
			case engine.MutantSurvived:
				f.Survivors = append(f.Survivors, Survivor{Position: m.Position, Operator: m.Operator})
			}
		}
		// A re-measure with unchanged pins keeps prior attestations that
		// still name the exact survivor; changed pins shed them, so every
		// evidence version's equivalences are re-judged (REQ-attest-survivor).
		if rec, ok := prior[targets[w.target].Symbol]; ok {
			if !sameAttestationPins(*rec, *f) {
				continue
			}
			open := map[survivorKey]bool{}
			for _, s := range f.Survivors {
				open[survivorKey{s.Position, s.Operator}] = true
			}
			for _, a := range rec.Attested {
				if open[survivorKey{a.Position, a.Operator}] {
					f.Attested = append(f.Attested, a)
				}
			}
		}
	}
	if repository.headMoved() {
		return nil, fmt.Errorf("gomutant: repository HEAD moved during mutation run")
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
	}
	return snapshot
}

func reportPreparation(callback func(PreparationEvent), event PreparationEvent) {
	if callback != nil {
		callback(event)
	}
}

func mergeFindingObservations(root string, env []string, states ...runtimeinput.State) (runtimeinput.State, error) {
	state, err := runtimeinput.MergeEnv(root, env, states...)
	if err == nil {
		return state, nil
	}
	result, incompleteErr := runtimeinput.IncompleteEnv(root, "runtime input observations could not be merged for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.State{}, incompleteErr
	}
	for _, input := range states {
		if input.Manifest == "" {
			continue
		}
		current, currentErr := runtimeinput.CurrentEnv(input.Manifest, root, env)
		if currentErr != nil || !current.OK {
			continue
		}
		merged, mergeErr := runtimeinput.MergeEnv(root, env, result, current)
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

func summarizeOperators(mutants []engine.Mutant, outcomes []engine.MutantOutcome) []OperatorSummary {
	byOperator := map[string]*OperatorSummary{}
	operators := make([]string, 0)
	for i, mutant := range mutants {
		summary := byOperator[mutant.Operator]
		if summary == nil {
			summary = &OperatorSummary{Operator: mutant.Operator}
			byOperator[mutant.Operator] = summary
			operators = append(operators, mutant.Operator)
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
