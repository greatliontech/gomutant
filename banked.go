package gomutant

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RunTallies is what a run counted about itself, delivered to
// Options.Tallies on every exit once measurement began — the first
// execution event, the first window's dispatch — and on every
// completed or drifted run; a run cancelled before that delivers
// nothing, since it has no banked state to report. The banked state
// a cancelled run reports and the narrowed-survivor audit's rate ride
// here, one tally for both faces (REQ-exec-banked-summary;
// REQ-exec-oracle-run's
// narrowed-survivor clause). Committed counts exactly the findings
// whose incremental commit RETURNED SUCCESSFULLY, with their kill and
// open tallies — never in-flight work, never a commit that failed —
// and stays zero for a caller that persists nothing.
type RunTallies struct {
	Committed int
	Killed    int
	Open      int
	Selected  int
	Served    int
	Skipped   int
	Audit     AuditSummary
}

// AuditSummary is the narrowed-survivor audit's measured rate: how
// many narrowed survivors the run re-scored under the full oracle and
// how many disagreed (each disagreement a false survivor re-scored).
type AuditSummary struct {
	Narrowed  int `json:"narrowed"`
	Disagreed int `json:"disagreed"`
}

// BankedState is a cancelled run's report of what the findings
// document kept: the exit cause and the tallies at the exit
// (REQ-exec-banked-summary). Both faces render it from Text.
type BankedState struct {
	Cause     string `json:"cause" jsonschema:"the exit cause: the graceful drain, the command timeout, an interrupt or cancellation, or an abort with its error"`
	Committed int    `json:"committed" jsonschema:"findings whose incremental commit returned successfully — exactly what the document holds from this run"`
	Killed    int    `json:"killed" jsonschema:"kills among the committed findings"`
	Open      int    `json:"open" jsonschema:"open survivors among the committed findings"`
	Selected  int    `json:"selected" jsonschema:"targets the selection held"`
	Served    int    `json:"served" jsonschema:"targets served from prior evidence before the exit"`
	Skipped   int    `json:"skipped" jsonschema:"targets skipped before the exit"`
	Elapsed   string `json:"elapsed" jsonschema:"time from the call's start to the exit"`
}

// Banked is the tallies as a cancelled run's banked state under the
// named cause, after the elapsed time the reporting face measured
// from its own start — the load and selection the run never saw
// included.
func (r RunTallies) Banked(cause string, elapsed time.Duration) BankedState {
	return BankedState{Cause: cause, Committed: r.Committed, Killed: r.Killed, Open: r.Open,
		Selected: r.Selected, Served: r.Served, Skipped: r.Skipped, Elapsed: elapsed.Round(time.Second).String()}
}

// PostCommitRenderBound bounds the rendering a run performs after its
// final replacement, which runs detached from the request's deadline
// (REQ-exec-cancellation's success boundary): long enough for the
// survivor cut over a large delta, short enough that a disconnected
// client never keeps a face rendering.
const PostCommitRenderBound = 2 * time.Minute

// PostCommitRenderContext is the context a face renders under after
// the final replacement returned: detached from the request's deadline
// and cancellation, bounded by PostCommitRenderBound — a deadline
// expiring after the commit never fails a committed run.
func PostCommitRenderContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), PostCommitRenderBound)
}

// Text renders the banked state as the one sentence both faces show.
func (b BankedState) Text() string {
	return fmt.Sprintf("%s after %s: %d target(s) committed to the findings document this run (%d killed, %d open among them); selection was %d target(s) — %d served, %d skipped before exit; every committed target is kept (REQ-exec-cancellation), the rest re-measure — a gracefully drained prefix extends — on the next run",
		b.Cause, b.Elapsed, b.Committed, b.Killed, b.Open, b.Selected, b.Served, b.Skipped)
}

// ExitCause names a run's non-drift exit for the banked state: the
// graceful drain, the command timeout, an interrupt or cancellation,
// or an abort carrying its error.
func ExitCause(err error) string {
	switch {
	case errors.Is(err, ErrInterrupted):
		// The graceful drain: measured prefixes are committed and
		// re-running the same command extends them — the one exit
		// that is neither a timeout, a hard cancel, nor an abort.
		return "interrupted gracefully - measured prefixes committed; re-run to extend"
	case errors.Is(err, context.DeadlineExceeded):
		return "command timeout"
	case errors.Is(err, context.Canceled):
		return "interrupt/cancellation"
	default:
		return fmt.Sprintf("aborted (%v)", err)
	}
}

// tallyCallbacks installs the run's own counting on the caller's
// callbacks: a commit that returns nil is banked with its finding's
// kill and open tallies (a caller supplying no commit persists
// nothing, so nothing is banked), served and skipped decisions are
// counted, the audit summary event's counts accumulate, and the first
// execution event — the first window's dispatch — marks measurement
// begun.
// Installed before the callback lock, so the counts ride the same
// serialization.
func tallyCallbacks(opts Options, tally *RunTallies, started *bool) Options {
	commit, decision, executing := opts.Commit, opts.Decision, opts.Executing
	if commit != nil {
		opts.Commit = func(f Finding) error {
			if err := commit(f); err != nil {
				return err
			}
			tally.Committed++
			tally.Killed += f.Killed
			tally.Open += len(f.Open())
			return nil
		}
	}
	opts.Decision = func(d RunDecision) {
		switch d.Action {
		case "cached":
			tally.Served++
		case "skipped":
			tally.Skipped++
		}
		if decision != nil {
			decision(d)
		}
	}
	opts.Executing = func(e ExecutionEvent) {
		*started = true
		if e.Phase == "audit" {
			tally.Audit.Narrowed += e.AuditedNarrowed
			tally.Audit.Disagreed += e.AuditDisagreed
		}
		if executing != nil {
			executing(e)
		}
	}
	return opts
}
