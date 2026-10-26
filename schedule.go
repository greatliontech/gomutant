package gomutant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/windowcost"
)

// coverageKey identifies the advisory full-pattern coverage probe.
func coverageKey(g group, coverPkg string) string {
	return g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg + "\x00" + strings.Join(g.flags, "\x00")
}

// executesCandidate is shared by dispatch and pricing. Reuse gates decide
// which candidates require fresh execution; coverage never makes that choice.
func executesCandidate(w work, mi int) bool {
	switch {
	case w.serve != nil && !w.flagged[mi]:
		return false
	case w.extend != nil && mi < w.extendFrom:
		return false
	}
	_, runnable := w.candidates[mi].Mutant()
	return runnable
}

func executingIndexes(w work) []int {
	var out []int
	for mi := range w.candidates {
		if executesCandidate(w, mi) {
			out = append(out, mi)
		}
	}
	return out
}

func stepBudget(g group, opts runOptions) time.Duration {
	if opts.groupBudget != nil {
		return opts.groupBudget(g)
	}
	return opts.OracleTimeout
}

// gatherWindow blocks for the next prepared work item and keeps blocking
// until the window's candidate budget is met or preparation ends. Blocking —
// never draining just what is buffered — is what keeps window boundaries a
// pure function of target order and the budget rule, independent of
// execution timing, so the window-scoped confirmation flip signal covers
// the same kills on every run (REQ-exec-attribution). ok is false when no
// item remains.
func gatherWindow(items <-chan work, ceiling, minimum int) ([]work, bool) {
	first, ok := <-items
	if !ok {
		return nil, false
	}
	window := []work{first}
	total, executions := len(first.candidates), windowcost.Executions(len(first.candidates), len(first.oracle))
	for total < ceiling && !(executions >= windowcost.ExecutionBudget && total >= minimum) {
		next, ok := <-items
		if !ok {
			break
		}
		window = append(window, next)
		total += len(next.candidates)
		executions += windowcost.Executions(len(next.candidates), len(next.oracle))
	}
	return window, true
}

// serveCheckRefusal renders a serve-time evidence-check failure as that
// target's own refusal reason: the check re-observes the view against
// disk when it closes over the record's runtime-input evidence, so a
// content move surfaces as a view-changed error and a move that breaks
// the typed load as a plain one — either way the condition is the
// target's, refused target-locally exactly as a measured target's
// post-execution validation failure (the measured path refuses on any
// non-ctx validateProducers error), never a campaign abort
// (REQ-exec-quiescence). Callers check ctx first; a truly
// campaign-fatal cause fails the run as N refusals, exactly as on the
// measured side. The reason claims drift only where the error
// establishes it — a view-changed failure; anything else passes
// verbatim, the measured path's own attribution discipline (the
// residue clause's misattribution class).
func serveCheckRefusal(err error) string {
	if errors.Is(err, gofresh.ErrViewChanged) {
		return "drift: the tree moved past the run-start view capture: " + err.Error()
	}
	return err.Error()
}

// commitFinding delivers one finished finding to the caller's incremental
// commit callback. The finding's capture commit was read at stamp time
// (stampProvenance), so the record already names the repository state its
// evidence was validated against — ref motion since is not grounds to
// discard completed evidence, and content drift was refused target-locally
// before the finding existed: measured paths validate their producers from
// disk before stamping, and serve paths re-observe the view at their own
// evidence checks (REQ-exec-quiescence,
// REQ-exec-cancellation).
func commitFinding(ctx context.Context, commit func(Finding) error, f Finding) error {
	if commit == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit(f)
}

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

// armed reports whether the gate confirms every kill: volatile
// evidence, a flip, or the opening streak. One predicate serves both
// the confirmation decision and the advisory mode label — two copies
// would let the label lie about the gate.
func (g *confirmationGate) armed() bool {
	return g.volatile || g.flipped || g.streak < windowcost.ConfirmStreak
}

// confirmNow reports whether the next kill confirms serially. An
// armed gate always confirms; otherwise every ConfirmStride-th kill
// confirms and the rest stride-skip.
func (g *confirmationGate) confirmNow() bool {
	if g.armed() {
		return true
	}
	g.sinceSample++
	if g.sinceSample >= windowcost.ConfirmStride {
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

// reportConfirmationFlip emits the loud demotion event: a provisional
// kill the serial re-run re-scored as a survivor names its mutant and
// withdrawn killer on every advisory face — a demotion is never
// silent (REQ-exec-run-status's confirmation-flip class).
func reportConfirmationFlip(executing func(ExecutionEvent), symbol, position, withdrawnKiller string, targetIndex, targetCount int) {
	reportExecuting(executing, ExecutionEvent{
		Phase:        "confirmation-flip",
		TargetIndex:  targetIndex,
		TargetCount:  targetCount,
		Symbol:       symbol,
		FlipPosition: position,
		FlipKiller:   withdrawnKiller,
	})
}

func reportExecuting(callback func(ExecutionEvent), event ExecutionEvent) {
	if callback != nil {
		callback(event)
	}
}

// readyWindow is one gathered-but-unexecuted window in the driver's
// ready pool: its works, its pre-probe ordering price, and whether
// every executing candidate priced. The pool is append-only with
// order-preserving deletes, so POOL ORDER IS ARRIVAL ORDER — the one
// fact nextReadyWindow's tie handling stands on.
type readyWindow struct {
	window []work
	cost   time.Duration
	priced bool
}

// nextReadyWindow picks the pool index to execute next: the cheapest
// fully-priced window; an unpriced window never jumps the queue on
// fabricated cheapness — it waits behind every priced window. Strict
// comparisons keep the FIRST of any tie, and pool order is arrival
// order, so equal-cost windows and unpriced windows alike run in
// gather order without any recorded sequence. The caller guarantees a
// non-empty pool.
func nextReadyWindow(pool []readyWindow) int {
	pick := 0
	for i := 1; i < len(pool); i++ {
		a, b := pool[i], pool[pick]
		switch {
		case a.priced != b.priced:
			if a.priced {
				pick = i
			}
		case a.priced && a.cost < b.cost:
			pick = i
		}
	}
	return pick
}

// advanceDone is the window-boundary true-up of the done counter: it
// trues up to base+total — the window's own candidate lens — but never
// below cur, because a drained window nils its discarded works'
// candidates before the true-up, and candidates that already ticked
// must not un-happen on any advisory face (REQ-exec-run-status's
// estimate class: ticks are monotone).
func advanceDone(base, total, cur int64) int64 {
	if next := base + total; next > cur {
		return next
	}
	return cur
}

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
func confirmWindowKills(volatile func(target int) bool, kills []windowKill, confirm func(windowKill, string) (confirmOutcome, error), unverifiable func(windowKill) bool) error {
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
	// mode names the gate state deciding the next confirmation — the
	// advisory vocabulary the events carry (ExecutionEvent.ConfirmationMode).
	mode := func(g *confirmationGate) string {
		if g.armed() {
			return "serial-full"
		}
		return "stride-sampled"
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
			outcome, err := confirm(next, mode(gateFor(next.target)))
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
