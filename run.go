package gomutant

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
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
}

// group is one test-binary invocation: a package, its oracle's -run
// pattern, and the binary's flags.
type group struct {
	pkgs                  []string
	runRegex              string
	flags                 []string
	moduleDir, packageDir string
}

// Run mutates each target and executes its oracle per mutant, fanning
// mutant runs across a worker pool (REQ-exec-oracle-run). Prior findings
// are served only when every target and oracle evidence record, operator,
// timeout, and budget pin holds, unless forced (REQ-result-stale). A run that
// cannot attribute an outcome aborts without findings
// (REQ-core-attributed-kills).
func (t *Tree) Run(ctx context.Context, targets []Target, opts Options) ([]Finding, error) {
	if opts.Budget < 0 {
		return nil, fmt.Errorf("gomutant: budget must be non-negative")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	repository := captureRepositoryState(t.dir)
	runEnv := t.eng.GoEnv()
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
	for i, tg := range targets {
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Labels: tg.Labels, OperatorSet: engine.OperatorSet, Timeout: opts.Timeout.String()}
		oracle := t.resolveOracle(tg)
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default).
			f.Skipped = "no oracle"
			continue
		}
		if err := t.eng.ValidateOracle(oracle); err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		bodyHash, err := t.eng.BodyHash(tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			f.Skipped = "not a function"
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		f.BodyHash = bodyHash
		targetView, err := t.newSubjectView(tg.Symbol)
		if err != nil {
			return nil, fmt.Errorf("target %s freshness: %w", tg.Symbol, err)
		}
		oracleViews := make([]*subjectView, 0, len(oracle))
		for _, symbol := range oracle {
			view, err := t.newSubjectView(symbol)
			if err != nil {
				return nil, fmt.Errorf("target %s oracle %s freshness: %w", tg.Symbol, symbol, err)
			}
			oracleViews = append(oracleViews, view)
		}

		if rec, ok := prior[tg.Symbol]; ok && !opts.Force && budgetCovers(rec.Budget, opts.Budget) {
			matches, err := evidenceSetMatches(*rec, targetView, oracleViews, engine.OperatorSet, opts.Timeout.String())
			if err != nil {
				return nil, err
			}
			if matches {
				cached := *rec
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
				findings[i] = cached
				continue
			}
		}

		mutants, err := t.eng.Mutants(tg.Symbol, opts.Budget)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
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
		rapidPkgs, _ := t.eng.SplitRapidPkgs(pkgs)
		rapid := make(map[string]bool, len(rapidPkgs))
		for _, p := range rapidPkgs {
			rapid[p] = true
		}
		var groups []group
		for _, pr := range runs {
			var flags []string
			if rapid[pr.pkg] {
				flags = []string{"-rapid.nofailfile"}
			}
			moduleDir, packageDir, err := t.eng.PackageContext(pr.pkg)
			if err != nil {
				return nil, err
			}
			groups = append(groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		pending = append(pending, work{target: i, mutants: mutants, groups: groups, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews})
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
				state, err := runtimeinput.MergeEnv(t.dir, runEnv, processStates...)
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

	// Phase three, sequential: aggregate in target and mutant order.
	for wi, w := range pending {
		f := &findings[w.target]
		states := append([]runtimeinput.State(nil), observations[wi]...)
		state, err := runtimeinput.MergeEnv(t.dir, runEnv, states...)
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
		sourceFiles := append([]string(nil), w.targetView.view.SourceFiles()...)
		for _, oracleView := range w.oracleViews {
			sourceFiles = append(sourceFiles, oracleView.view.SourceFiles()...)
		}
		sourceFiles = append(sourceFiles, repository.historicalPackageFiles(sourceFiles)...)
		sourceFiles = withModuleSelectionPaths(sourceFiles)
		sourceFiles = append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum"))
		f.Dirty = repository.pathsDirty(sourceFiles, state)
		for mi, m := range w.mutants {
			switch outcomes[wi][mi] {
			case engine.MutantDiscarded:
				f.Discarded++
			case engine.MutantKilled:
				f.Mutants++
				f.Killed++
			case engine.MutantSurvived:
				f.Mutants++
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
