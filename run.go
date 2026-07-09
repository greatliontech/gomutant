package gomutant

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

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
	pkgs     []string
	runRegex string
	flags    []string
}

// Run mutates each target and executes its oracle per mutant, fanning
// mutant runs across a worker pool (REQ-exec-oracle-run). Prior findings
// are served only when every pin holds — body hash, oracle content,
// operator set, toolchain, and a covering budget — unless forced
// (REQ-result-stale). A run that cannot attribute an outcome aborts without
// findings (REQ-core-attributed-kills).
func (t *Tree) Run(ctx context.Context, targets []Target, opts Options) ([]Finding, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
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

	toolchain, err := engine.Toolchain(t.dir)
	if err != nil {
		return nil, err
	}

	// Phase one, sequential: resolve every target to a terminal finding
	// (skipped, cached) or to a mutant work list.
	type work struct {
		target    int
		mutants   []engine.Mutant
		groups    []group
		oracleSet map[string]bool
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
		*f = Finding{Symbol: tg.Symbol, Labels: tg.Labels, OperatorSet: engine.OperatorSet, Toolchain: toolchain}
		oracle := t.resolveOracle(tg)
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default).
			f.Skipped = "no oracle"
			continue
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
		// Pin each oracle test by the body hash it ran at
		// (REQ-result-record).
		pins := make([]OraclePin, 0, len(oracle))
		for _, o := range oracle {
			oh, err := t.eng.BodyHash(o)
			if err != nil {
				return nil, fmt.Errorf("target %s oracle %s: %w", tg.Symbol, o, err)
			}
			pins = append(pins, OraclePin{Symbol: o, Hash: oh})
		}
		f.Oracle = pins

		if rec, ok := prior[tg.Symbol]; ok && !opts.Force &&
			pinsMatch(*rec, bodyHash, pins, engine.OperatorSet, toolchain) &&
			budgetCovers(rec.Budget, opts.Budget) {
			f.Cached = true
			f.BodyLine = rec.BodyLine
			f.Budget = rec.Budget
			f.Mutants = rec.Mutants
			f.Killed = rec.Killed
			f.Discarded = rec.Discarded
			f.Survivors = append([]Survivor(nil), rec.Survivors...)
			f.Attested = append([]Attestation(nil), rec.Attested...)
			continue
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
		if len(mutants) > 0 {
			f.BodyLine = mutants[0].BodyLine
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
			groups = append(groups, group{[]string{pr.pkg}, pr.runRegex, flags})
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		pending = append(pending, work{target: i, mutants: mutants, groups: groups, oracleSet: oracleSet})
	}

	// Phase two: the pool. Outcomes land in a preallocated matrix so
	// aggregation is deterministic regardless of completion order; the first
	// error cancels everything in flight.
	outcomes := make([][]engine.MutantOutcome, len(pending))
	for wi := range pending {
		outcomes[wi] = make([]engine.MutantOutcome, len(pending[wi].mutants))
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
				for _, g := range w.groups {
					if outcome != engine.MutantSurvived {
						break
					}
					out, killer, err := engine.RunMutant(poolCtx, t.dir, m, g.pkgs, g.runRegex, opts.Timeout, g.flags)
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
		// still name a survivor; changed pins shed them, so every body
		// version's equivalences are re-judged (REQ-attest-survivor). Old
		// positions rebase against the recorded declaration anchor first.
		if rec, ok := prior[targets[w.target].Symbol]; ok &&
			pinsMatch(*rec, f.BodyHash, f.Oracle, engine.OperatorSet, toolchain) {
			delta := f.BodyLine - rec.BodyLine
			open := map[string]bool{}
			for _, s := range f.Survivors {
				open[s.Position+"|"+s.Operator] = true
			}
			for _, a := range rec.Attested {
				pos, ok := shiftPos(a.Position, delta)
				if ok && open[pos+"|"+a.Operator] {
					f.Attested = append(f.Attested, Attestation{Position: pos, Operator: a.Operator, Reason: a.Reason})
				}
			}
		}
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
