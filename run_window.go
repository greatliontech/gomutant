package gomutant

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/trace"
	"github.com/greatliontech/gomutant/internal/windowcost"
)

func (r *campaignRunner) executeWindow(ctx context.Context, window []work) (*executedWindow, error) {
	t := r.t
	opts := r.opts
	targets := r.targets
	findings := r.findings
	runEnv := r.runEnv
	jobs := r.jobs

	type job struct {
		wi, mi int
		w      work
	}
	schemaGroups := make([][]engine.Candidate, 0, len(window))
	for wi := range window {
		if !window[wi].shaped && len(window[wi].candidates) > 0 {
			schemaGroups = append(schemaGroups, window[wi].candidates)
		}
	}
	if len(schemaGroups) > 1 {
		engine.MergeCandidateSchemas(schemaGroups...)
	}
	outcomes := make([][]engine.MutantOutcome, len(window))
	flips := make([]map[int]string, len(window))
	observations := make([][]gofresh.Observation, len(window))
	incompletes := make([][]string, len(window))
	killers := make([][]string, len(window))
	memoryDecided := make([][]bool, len(window))
	for wi := range window {
		outcomes[wi] = make([]engine.MutantOutcome, len(window[wi].candidates))
		observations[wi] = make([]gofresh.Observation, len(window[wi].candidates))
		incompletes[wi] = make([]string, len(window[wi].candidates))
		killers[wi] = make([]string, len(window[wi].candidates))
		memoryDecided[wi] = make([]bool, len(window[wi].candidates))
	}
	score := func(wi, mi int, outcome engine.MutantOutcome, killer string, decided bool, state gofresh.Observation, incomplete string) {
		killers[wi][mi] = killer
		memoryDecided[wi][mi] = decided
		observations[wi][mi] = r.interner.intern(state)
		incompletes[wi][mi] = incomplete
		outcomes[wi][mi] = outcome
	}
	windowBase := r.mutantsDone.Load()
	reportExecuting(opts.Executing, ExecutionEvent{
		Phase:       "executing",
		TargetIndex: r.dispatchedTargets + 1, TargetCount: int(r.preparedTargets.Load()),
		Symbol:         targets[window[0].target].Symbol,
		CandidatesDone: int(windowBase), CandidatesTotal: int(r.preparedCandidates.Load()),
	})
	est := estimateWindow(window, opts.baselineDur)
	reportExecuting(opts.Executing, ExecutionEvent{
		Phase:       "estimate",
		TargetIndex: r.dispatchedTargets + 1, TargetCount: int(r.preparedTargets.Load()),
		Symbol:         targets[window[0].target].Symbol,
		CandidatesDone: int(windowBase), CandidatesTotal: int(r.preparedCandidates.Load()),
		EstimateFull: est.full, EstimateUnknown: est.unknown,
		EstimateProjected: est.projectedString(),
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
				w := j.w
				m, runnable := w.candidates[j.mi].Mutant()
				if !runnable {
					continue
				}
				outcome, killer, candidateMemoryDecided, state, incompleteReason, err := t.executeWorkMutant(poolCtx, w, m, opts, runEnv)
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
				score(j.wi, j.mi, outcome, killer, candidateMemoryDecided, state, incompleteReason)
				r.tickMu.Lock()
				reportExecuting(opts.Executing, ExecutionEvent{
					Phase:       "tick",
					TargetIndex: r.dispatchedTargets + j.wi + 1, TargetCount: int(r.preparedTargets.Load()),
					Symbol:         w.candidates[j.mi].Symbol,
					CandidatesDone: int(r.mutantsDone.Add(1)), CandidatesTotal: int(r.preparedCandidates.Load()),
				})
				r.tickMu.Unlock()
			}
		}()
	}
	softStopped := false
	breakWi := len(window)
	prefixes := make([]int, len(window))
	for wi := range prefixes {
		prefixes[wi] = -1
	}
dispatching:
	for wi := range window {
		for mi := range window[wi].candidates {
			select {
			case <-opts.SoftStop:
				softStopped = true
				prefixes[wi], breakWi = mi, wi
				break dispatching
			default:
			}
			if !executesCandidate(window[wi], mi) {
				continue
			}
			if opts.dispatched != nil {
				opts.dispatched(window[wi].candidates[mi].Symbol, mi)
			}
			select {
			case jobCh <- job{wi, mi, window[wi]}:
			case <-opts.SoftStop:
				softStopped = true
				prefixes[wi], breakWi = mi, wi
				break dispatching
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
	var discard []bool
	if softStopped {
		discard = make([]bool, len(window))
		for wi := range window {
			k := prefixes[wi]
			if wi > breakWi {
				k = 0
			}
			if k < 0 {
				continue
			}
			if window[wi].serve != nil || window[wi].extend != nil || k == 0 {
				discard[wi] = true
				window[wi].candidates = nil
				outcomes[wi], observations[wi], incompletes[wi], killers[wi], memoryDecided[wi] = nil, nil, nil, nil, nil
				continue
			}
			window[wi].candidates = window[wi].candidates[:k]
			outcomes[wi] = outcomes[wi][:k]
			observations[wi] = observations[wi][:k]
			incompletes[wi] = incompletes[wi][:k]
			killers[wi] = killers[wi][:k]
			memoryDecided[wi] = memoryDecided[wi][:k]
			f := &findings[window[wi].target]
			f.Budget = k
			f.Generated = k
		}
	}
	windowTotal := int64(0)
	for wi := range window {
		windowTotal += int64(len(window[wi].candidates))
	}
	r.mutantsDone.Store(advanceDone(windowBase, windowTotal, r.mutantsDone.Load()))
	rescore := func(wi, mi int, run func(w work, m engine.Mutant) (engine.MutantOutcome, string, bool, gofresh.Observation, string, error)) (engine.MutantOutcome, string, error) {
		w := window[wi]
		m, _ := window[wi].candidates[mi].Mutant()
		opts.probeGate.Lock()
		outcome, killer, decided, state, incomplete, err := run(w, m)
		opts.probeGate.Unlock()
		if err != nil {
			return outcome, killer, err
		}
		score(wi, mi, outcome, killer, decided, state, incomplete)
		return outcome, killer, nil
	}
	if jobs > 1 {
		confirmTotal := 0
		for wi := range window {
			for mi := range window[wi].candidates {
				if outcomes[wi][mi] == engine.MutantKilled && killers[wi][mi] != engine.TimeoutKiller {
					if _, runnable := window[wi].candidates[mi].Mutant(); runnable {
						confirmTotal++
					}
				}
			}
		}
		confirmDone := 0
		var kills []windowKill
		for wi := range window {
			for mi := range window[wi].candidates {
				if outcomes[wi][mi] != engine.MutantKilled || killers[wi][mi] == engine.TimeoutKiller {
					continue
				}
				if _, runnable := window[wi].candidates[mi].Mutant(); !runnable {
					continue
				}
				kills = append(kills, windowKill{target: wi, mi: mi})
			}
		}
		confirmStart := time.Now()
		var lastConfirmMode string
		volatileMemo := map[int]bool{}
		err := confirmWindowKills(
			func(wi int) bool {
				v, ok := volatileMemo[wi]
				if !ok {
					v = windowEvidenceVolatile(&window[wi], observations[wi])
					volatileMemo[wi] = v
				}
				return v
			},
			kills,
			func(k windowKill, confirmMode string) (confirmOutcome, error) {
				lastConfirmMode = confirmMode
				if err := ctx.Err(); err != nil {
					return confirmInconclusive, err
				}
				wi := k.target
				reportExecuting(opts.Executing, ExecutionEvent{
					Phase:              "confirming",
					TargetIndex:        r.dispatchedTargets + wi + 1,
					TargetCount:        int(r.preparedTargets.Load()),
					Symbol:             targets[window[wi].target].Symbol,
					CandidatesDone:     int(r.mutantsDone.Load()),
					CandidatesTotal:    int(r.preparedCandidates.Load()),
					ConfirmationsDone:  confirmDone,
					ConfirmationsTotal: confirmTotal,
					ConfirmationMode:   confirmMode,
				})
				initialKiller := killers[wi][k.mi]
				outcome, killer, err := rescore(wi, k.mi, func(w work, m engine.Mutant) (engine.MutantOutcome, string, bool, gofresh.Observation, string, error) {
					return t.confirmMutant(ctx, w, m, initialKiller, r.scopedBaselines, opts, runEnv)
				})
				if err != nil {
					return confirmInconclusive, err
				}
				confirmDone++
				classified := classifyConfirmation(outcome, killer)
				if classified == confirmFlipped {
					if flips[wi] == nil {
						flips[wi] = map[int]string{}
					}
					flips[wi][k.mi] = initialKiller
					reportConfirmationFlip(opts.Executing,
						targets[window[wi].target].Symbol,
						window[wi].candidates[k.mi].Position,
						initialKiller,
						r.dispatchedTargets+wi+1, int(r.preparedTargets.Load()))
				}
				return classified, nil
			},
			func(k windowKill) bool {
				return observations[k.target][k.mi].Unverifiable
			},
		)
		if trace.AnyEnabled() {
			var firstSymbol string
			if len(window) > 0 {
				firstSymbol = targets[window[0].target].Symbol
			}
			trace.Emit(ctx, trace.DomainOracle, "window_confirm", firstSymbol,
				trace.String("mode", lastConfirmMode),
				trace.Int("kills", len(kills)),
				trace.Int("confirmations", confirmDone),
				trace.Int64("duration_ms", time.Since(confirmStart).Milliseconds()),
				trace.Err(err),
			)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cancel()
	r.dispatchedTargets += len(window)
	scores := make([]windowScores, len(window))
	for wi := range window {
		scores[wi] = windowScores{outcomes: outcomes[wi], killers: killers[wi], flips: flips[wi], memoryDecided: memoryDecided[wi]}
	}
	return &executedWindow{window: window, scores: scores, observations: observations, incompletes: incompletes, discard: discard}, nil
}

func (r *campaignRunner) runExecutionPipeline(ctx context.Context) ([]Finding, error) {
	items := make(chan work, len(r.targets))
	var prepErr error
	prepDone := make(chan struct{})
	runCtx, cancelRun := context.WithCancel(ctx)
	defer func() {
		cancelRun()
		<-prepDone
	}()
	go func() {
		defer close(prepDone)
		defer close(items)
		delivered := 0
		err := r.deliverPrepared(runCtx, func(w work) error {
			if seams.truncateAfterItems > 0 && delivered >= seams.truncateAfterItems {
				return errTruncateSeam
			}
			select {
			case items <- w:
				delivered++
				return nil
			case <-runCtx.Done():
				return runCtx.Err()
			}
		})
		if errors.Is(err, errTruncateSeam) {
			err = seams.truncateErr
		}
		prepErr = err
	}()
	windowBudget, windowMinimum := windowcost.Bounds(r.jobs, seams.windowCandidates)
	var treeDrift error
	var held *executedWindow
	aggregateHeld := func() error {
		if held == nil {
			return nil
		}
		err := r.aggregateWindow(ctx, held)
		held = nil
		return err
	}
	windowCh := make(chan []work, len(r.targets))
	go func() {
		defer close(windowCh)
		for {
			window, ok := gatherWindow(items, windowBudget, windowMinimum)
			if !ok {
				return
			}
			windowCh <- window
		}
	}()
	mkReady := func(window []work) readyWindow {
		cost, priced := windowPrice(window, r.opts.baselineDur)
		return readyWindow{window: window, cost: cost, priced: priced}
	}
	var pool []readyWindow
	windowChOpen := true
	for {
		if len(pool) == 0 {
			if !windowChOpen {
				break
			}
			window, ok := <-windowCh
			if !ok {
				break
			}
			pool = append(pool, mkReady(window))
		}
		if windowChOpen {
			if seams.waitPreparedBeforePick {
				<-prepDone
			}
			select {
			case <-prepDone:
				for window := range windowCh {
					pool = append(pool, mkReady(window))
				}
				windowChOpen = false
			default:
			drainReady:
				for {
					select {
					case window, ok := <-windowCh:
						if !ok {
							windowChOpen = false
							break drainReady
						}
						pool = append(pool, mkReady(window))
					default:
						break drainReady
					}
				}
			}
		}
		select {
		case <-prepDone:
			if prepErr != nil {
				if err := aggregateHeld(); err != nil {
					return nil, err
				}
				return nil, prepErr
			}
		default:
		}
		pick := nextReadyWindow(pool)
		window := pool[pick].window
		copy(pool[pick:], pool[pick+1:])
		pool[len(pool)-1] = readyWindow{}
		pool = pool[:len(pool)-1]
		if err := aggregateHeld(); err != nil {
			return nil, err
		}
		executed, err := r.executeWindow(ctx, window)
		if err != nil {
			return nil, err
		}
		if executed.discard != nil {
			if err := r.aggregateWindow(ctx, executed); err != nil {
				return nil, err
			}
			return nil, ErrInterrupted
		}
		held = executed
	}
	<-prepDone
	if prepErr != nil {
		if err := aggregateHeld(); err != nil {
			return nil, err
		}
		return nil, prepErr
	}
	if r.opts.afterExecution != nil {
		r.opts.afterExecution()
	}
	if err := r.eachMode(func(mv *modeViews) error {
		return mv.views.validateProducers(ctx)
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		treeDrift = err
	}
	if held != nil {
		if err := r.aggregateWindow(ctx, held); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	driftedSymbols := make(map[string]bool, len(r.drifted))
	for _, d := range r.drifted {
		driftedSymbols[d.Symbol] = true
	}
	var unfinished []string
	for i := range r.findings {
		if r.findings[i].Cached || r.findings[i].Skipped != "" || r.committed[i] || driftedSymbols[r.targets[i].Symbol] {
			continue
		}
		unfinished = append(unfinished, r.targets[i].Symbol)
	}
	if len(unfinished) > 0 {
		return nil, fmt.Errorf("gomutant: campaign truncated - %s ended without a result and without an error; the findings document keeps each unfinished target's prior record", cappedNameList(unfinished, "target"))
	}
	if r.opts.Posture != nil {
		r.emitPostures(ctx, driftedSymbols)
	}
	if len(r.drifted) > 0 || treeDrift != nil {
		completed := completedFindings(r.findings, r.drifted)
		if len(r.drifted) == 0 {
			return completed, &TreeDriftError{Completed: len(completed), Transient: treeDrift.Error()}
		}
		return completed, &TreeDriftError{Drifted: r.drifted, Completed: len(completed)}
	}
	return r.findings, nil
}
