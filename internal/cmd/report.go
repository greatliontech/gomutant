package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomutant "github.com/greatliontech/gomutant"

	"github.com/spf13/pflag"
)

// runReporter is the run's cumulative reporting state: the cadenced
// progress line, the banked-state exit summary, and the optional
// JSON-lines face all read from it. Everything here is advisory
// rendering — nothing enters a decision or finding
// (REQ-exec-run-status's advisory class) — and the banked-state
// summary derives ONLY from findings whose incremental commit
// returned: an interrupted run never reports work the document does
// not hold (REQ-exec-cancellation).
type runReporter struct {
	mu    sync.Mutex
	out   io.Writer
	jsonl bool
	start time.Time

	selected int // targets in the filtered selection
	served   int // decisions: cached
	skipped  int // decisions: skipped
	measure  int // decisions: measure

	committed    int // findings whose incremental commit returned
	bankedKilled int
	bankedOpen   int
	lastExec     gomutant.ExecutionEvent
	// auditedNarrowed/auditDisagreed accumulate the narrowed-survivor
	// audit's per-window events for the run-summary rate line.
	auditedNarrowed int
	auditDisagreed  int
	// paceStart/paceBase/paceDone anchor the measured execution pace:
	// first completion tick to latest, so the progress line's
	// estimated-remaining reflects mutant execution alone —
	// preparation and baselines never dilute it.
	paceStart time.Time
	paceBase  int
	paceDone  int
	// now is the reporter's clock — a field so the pace arithmetic is
	// testable against a fixed instant instead of a wall-clock race.
	now           func() time.Time
	lastMode      map[string]string // symbol -> last confirmation mode rendered
	stopCadence   chan struct{}
	cadenceDone   chan struct{}
	cadenceClosed sync.Once
	writeErr      error // first structured-face write failure, surfaced at exit
	// phaseLabel names the stretch in flight before any execution
	// event — loading, an ephemeral probe's phase, a judged record —
	// so the cadence line says what the process is doing while the
	// run-shaped tallies are all zero (REQ-exec-run-status).
	phaseLabel atomic.Value
	// decided marks the first decision: from then on the tallies are the
	// cadence line, whatever stretch a later preparation event names.
	decided atomic.Bool
}

// phase names the stretch now in flight for the cadence line; the
// label yields to the run tallies at the first decision — from then
// on the served, skipped, and committed counts are the forward-progress
// signal, whatever stretch is in flight.
func (r *runReporter) phase(label string) { r.phaseLabel.Store(label) }

// phaseInFlight is the primed label while no decision has been
// reported, else empty.
func (r *runReporter) phaseInFlight() string {
	if r.decided.Load() {
		return ""
	}
	label, _ := r.phaseLabel.Load().(string)
	return label
}

// preparation reports a preparation event on the reporter's face — the
// structured record, or the human prepare line — and primes the phase
// label with it (REQ-exec-run-status).
func (r *runReporter) preparation(event gomutant.PreparationEvent) {
	r.phase(event.Text())
	r.line("prepare", event, func(w io.Writer) { renderPreparation(w, event) })
}

// epilogue renders a verb's result rows: the cadence stops and joins
// first, so no progress line can trail them — a result cannot be
// rendered without ending the cadence.
func (r *runReporter) epilogue(render func(io.Writer)) {
	r.stop()
	render(r.out)
}

// interrupted reports the stretch an interruption cut short, on the
// reporter's face, after the cadence has stopped.
func (r *runReporter) interrupted(cause string) {
	label := r.phaseInFlight()
	elapsed := time.Since(r.start).Round(time.Second)
	r.line("interrupted", map[string]string{"cause": cause, "phase": label, "elapsed": elapsed.String()}, func(w io.Writer) {
		fmt.Fprintf(w, "interrupted  %s during %s after %s\n", cause, label, elapsed)
	})
}

// verbProgressInterval is the cadence of the phase-naming progress
// line on the verbs whose cost is one call (a load, a judged record, a
// prune, a retarget): the run and ephemeral verbs expose the same
// cadence as a flag because their stretches are the caller's to size.
// A variable so a test can lower it — the package's tests run
// serially, so the swap is race-free.
var verbProgressInterval = gomutant.ProgressCadence

// progressIntervalFlag registers a verb's cadence flag: one name and one
// default (the shared cadence) on every verb that exposes it, the usage
// the verb's own.
func progressIntervalFlag(f *pflag.FlagSet, into *time.Duration, usage string) {
	f.DurationVar(into, "progress-interval", gomutant.ProgressCadence, usage)
}

func newRunReporter(out io.Writer, jsonl bool, selected int) *runReporter {
	return &runReporter{
		out: out, jsonl: jsonl, start: time.Now(), selected: selected, now: time.Now,
		lastMode: map[string]string{}, stopCadence: make(chan struct{}),
	}
}

// emit writes one JSON line with the event kind stitched in. Human
// rendering stays with the per-event render functions; this is the
// structured face's single choke point.
func (r *runReporter) emit(kind string, payload any) {
	env := map[string]any{}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err == nil {
			var fields map[string]any
			if json.Unmarshal(raw, &fields) == nil {
				for k, v := range fields {
					env[k] = v
				}
			}
		}
	}
	// The kind is stamped LAST: no payload field may clobber it.
	env["event"] = kind
	line, err := json.Marshal(env)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintln(r.out, string(line)); err != nil {
		r.mu.Lock()
		if r.writeErr == nil {
			// The structured face must not fail silently on a broken
			// pipe: the first write error surfaces at command exit.
			r.writeErr = err
		}
		r.mu.Unlock()
	}
}

// firstWriteError reports the first structured-face write failure.
func (r *runReporter) firstWriteError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeErr
}

// line renders one human-face line unless the structured face owns
// the stream; the two faces never interleave.
func (r *runReporter) line(kind string, payload any, human func(io.Writer)) {
	if r.jsonl {
		r.emit(kind, payload)
		return
	}
	human(r.out)
}

// setSelected records the filtered selection size once targets are
// known — the reporter is constructed before discovery so the very
// first preparation line already rides the structured face.
func (r *runReporter) setSelected(n int) {
	r.mu.Lock()
	r.selected = n
	r.mu.Unlock()
}

func (r *runReporter) decision(d gomutant.RunDecision) {
	r.decided.Store(true)
	r.mu.Lock()
	switch d.Action {
	case "cached":
		r.served++
	case "skipped":
		r.skipped++
	case "measure":
		r.measure++
	}
	r.mu.Unlock()
}

func (r *runReporter) executing(e gomutant.ExecutionEvent) {
	r.mu.Lock()
	r.lastExec = e
	if e.Phase == "tick" {
		// The pace anchor: elapsed-per-candidate measures from the
		// FIRST completion, so preparation and baseline time never
		// dilute the pace the estimate reconciles against.
		if r.paceStart.IsZero() {
			r.paceStart = r.now()
			r.paceBase = e.CandidatesDone - 1
		}
		r.paceDone = e.CandidatesDone
	}
	if e.Phase == "audit" {
		r.auditedNarrowed += e.AuditedNarrowed
		r.auditDisagreed += e.AuditDisagreed
	}
	r.mu.Unlock()
}

// auditTotals reports the run's accumulated narrowed-survivor audit
// counts for the summary line (REQ-exec-oracle-run's narrowed-survivor
// clause: the measured disagreement rate rides the run summary).
func (r *runReporter) auditTotals() (audited, disagreed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.auditedNarrowed, r.auditDisagreed
}

// confirmationModeSuffix reports the suffix to append to a confirming
// line: the mode, rendered when it first appears for a symbol or
// changes mid-target — the disarmed stride is otherwise
// indistinguishable from the armed one in the log.
func (r *runReporter) confirmationModeSuffix(e gomutant.ExecutionEvent) string {
	if e.ConfirmationMode == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastMode[e.Symbol] == e.ConfirmationMode {
		return ""
	}
	r.lastMode[e.Symbol] = e.ConfirmationMode
	return "  mode=" + e.ConfirmationMode
}

// selectionNote renders the "(of K selected)" context for an
// execution line: shown whenever the prepared-target denominator is
// not the whole selection OR serves/skips have offset it — equality
// alone can be coincidental, and the context must not vanish exactly
// when it is ambiguous.
func (r *runReporter) selectionNote(targetCount int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.selected == 0 {
		return ""
	}
	if targetCount == r.selected && r.served+r.skipped == 0 {
		return ""
	}
	return fmt.Sprintf(" (of %d selected)", r.selected)
}

// banked records one incrementally committed finding — the only
// evidence the exit summary may claim.
func (r *runReporter) bankedFinding(f gomutant.Finding) {
	r.mu.Lock()
	r.committed++
	r.bankedKilled += f.Killed
	r.bankedOpen += len(f.Open())
	r.mu.Unlock()
}

type progressPayload struct {
	TargetsDone     int    `json:"targetsDone"`
	Selected        int    `json:"selected"`
	Served          int    `json:"served"`
	Skipped         int    `json:"skipped"`
	CandidatesDone  int    `json:"candidatesDone"`
	CandidatesTotal int    `json:"candidatesTotal"`
	Killed          int    `json:"killed"`
	Open            int    `json:"open"`
	Elapsed         string `json:"elapsed"`
	// EstRemaining extrapolates the measured execution pace (first
	// completion tick to the latest) over the remaining prepared
	// candidates — advisory, absent until at least one candidate
	// completed, and honest about its denominator: the prepared
	// total grows while preparation pipelines.
	EstRemaining string `json:"estRemaining,omitempty"`
}

func (r *runReporter) progressSnapshot() progressPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return progressPayload{
		// One population on both sides of the slash: commits (cached
		// serves included) against the SELECTION — the denominator an
		// operator knows before the first execution event, so the
		// line is never 0/0 through a long preparation or an
		// all-served resume. Skipped targets never commit, so a
		// selection with skips tops out below N/N by design — the
		// skipped split beside it says why.
		TargetsDone: r.committed,
		Selected:    r.selected, Served: r.served, Skipped: r.skipped,
		CandidatesDone: r.lastExec.CandidatesDone, CandidatesTotal: r.lastExec.CandidatesTotal,
		Killed: r.bankedKilled, Open: r.bankedOpen,
		Elapsed:      time.Since(r.start).Round(time.Second).String(),
		EstRemaining: r.estRemainingLocked(),
	}
}

// estRemainingLocked extrapolates the measured execution pace over
// the remaining prepared candidates; empty until a completion tick
// anchored the pace — the line states nothing it has not measured.
// Caller holds r.mu.
func (r *runReporter) estRemainingLocked() string {
	done := r.paceDone - r.paceBase
	if r.paceStart.IsZero() || done <= 0 {
		return ""
	}
	remaining := r.lastExec.CandidatesTotal - r.paceDone
	if remaining <= 0 {
		return ""
	}
	perCandidate := r.now().Sub(r.paceStart) / time.Duration(done)
	return (perCandidate * time.Duration(remaining)).Round(time.Second).String()
}

func (r *runReporter) progressLine() {
	if label := r.phaseInFlight(); label != "" {
		elapsed := time.Since(r.start).Round(time.Second)
		r.line("progress", map[string]string{"phase": label, "elapsed": elapsed.String()}, func(w io.Writer) {
			fmt.Fprintf(w, "progress  %s, elapsed %s\n", label, elapsed)
		})
		return
	}
	p := r.progressSnapshot()
	r.line("progress", p, func(w io.Writer) {
		line := fmt.Sprintf("progress  %d/%d targets committed (%d served, %d skipped), candidates %d/%d, %d killed, %d open, elapsed %s",
			p.TargetsDone, p.Selected, p.Served, p.Skipped,
			p.CandidatesDone, p.CandidatesTotal, p.Killed, p.Open, p.Elapsed)
		if p.EstRemaining != "" {
			line += ", est ~" + p.EstRemaining + " remaining (pace)"
		}
		fmt.Fprintln(w, line)
	})
}

// startCadence emits the compact progress line on a fixed cadence
// until stopped — the field ask: a run's health readable from the log
// without decoding per-confirmation noise.
func (r *runReporter) startCadence(interval time.Duration) {
	if interval <= 0 {
		return
	}
	r.cadenceDone = make(chan struct{})
	go func() {
		defer close(r.cadenceDone)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-r.stopCadence:
				return
			case <-t.C:
				r.progressLine()
			}
		}
	}()
}

// stop ends the cadence AND joins its goroutine: after stop returns,
// no progress line can trail the epilogue or write to a
// caller-supplied writer the command has returned from.
func (r *runReporter) stop() {
	r.cadenceClosed.Do(func() { close(r.stopCadence) })
	if r.cadenceDone != nil {
		<-r.cadenceDone
	}
}

type bankedPayload struct {
	Cause     string `json:"cause"`
	Committed int    `json:"committed"`
	Killed    int    `json:"killed"`
	Open      int    `json:"open"`
	Selected  int    `json:"selected"`
	Served    int    `json:"served"`
	Skipped   int    `json:"skipped"`
	Elapsed   string `json:"elapsed"`
}

// bankedState renders the exit summary on a non-drift error path:
// what the findings document holds (incrementally committed
// findings), what the run had dispositioned, and the cause — a budget
// or signal exit never ends on a bare context error again.
func (r *runReporter) bankedState(cause string) {
	r.mu.Lock()
	p := bankedPayload{
		Cause: cause, Committed: r.committed,
		Killed: r.bankedKilled, Open: r.bankedOpen,
		Selected: r.selected, Served: r.served, Skipped: r.skipped,
		Elapsed: time.Since(r.start).Round(time.Second).String(),
	}
	r.mu.Unlock()
	r.line("banked", p, func(w io.Writer) {
		fmt.Fprintf(w, "banked    %s after %s: %d target(s) committed to the findings document this run (%d killed, %d open among them); selection was %d target(s) — %d served, %d skipped before exit; every committed target is kept (REQ-exec-cancellation), the rest re-measure — a gracefully drained prefix extends — on the next run\n",
			p.Cause, p.Elapsed, p.Committed, p.Killed, p.Open, p.Selected, p.Served, p.Skipped)
	})
}

// flushProse delivers accumulated human epilogue prose: verbatim on
// the human face, wrapped as note events on the structured one — the
// structured stream never loses a line the human face would show,
// and no raw line leaks into it.
func (r *runReporter) flushProse(text string) error {
	if !r.jsonl {
		_, err := io.WriteString(r.out, text)
		return err
	}
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if line != "" {
			r.emit("note", map[string]string{"text": line})
		}
	}
	// Every flush site inherits the broken-pipe plumb: an early exit
	// (no-targets, plan) must fail on a truncated structured stream
	// exactly as the main exit does.
	return r.firstWriteError()
}

// analysisVocabulary maps gofresh's internal phase names to the
// operator vocabulary — an unexplained "analysis observe" was the
// field report's exact complaint. Unknown phases pass through raw so
// new engine vocabulary stays visible rather than silently renamed.
var analysisVocabulary = map[string]string{
	"analysis-unavailable": "attributed reachability unavailable for",
	"load":                 "loading package graphs (gofresh analysis)",
	"observe":              "observing oracle runtime inputs (freshness evidence)",
	"runtime":              "validating runtime-input evidence (oracle freshness)",
	"prove":                "proving oracle closure freshness (gofresh hash proof)",
	"baseline-output":      "oracle baseline output for",
}

func analysisPhrase(phase string) string {
	if v, ok := analysisVocabulary[phase]; ok {
		return v
	}
	return phase
}

// resultRowPayload is the structured face's per-target result row —
// the same facts as the human measured/cached row plus its survivors
// and operator tallies inline, and the persistence layer when the
// record stayed machine-local (REQ-exec-run-status,
// REQ-result-layers).
type resultRowPayload struct {
	Symbol      string                     `json:"symbol"`
	Cached      bool                       `json:"cached"`
	Generated   int                        `json:"generated"`
	Candidates  int                        `json:"candidates"`
	Mutants     int                        `json:"mutants"`
	Killed      int                        `json:"killed"`
	Discarded   int                        `json:"discarded"`
	Open        []survivorRowPayload       `json:"open,omitempty"`
	Operators   []gomutant.OperatorSummary `json:"operators,omitempty"`
	Layer       string                     `json:"layer,omitempty"`
	LayerReason string                     `json:"layerReason,omitempty"`
}

type survivorRowPayload struct {
	Position  string `json:"position"`
	Operator  string `json:"operator"`
	Execution string `json:"execution,omitempty"`
}

func resultRow(f gomutant.Finding, layer, layerReason string) resultRowPayload {
	row := resultRowPayload{
		Symbol: f.Symbol, Cached: f.Cached,
		Generated: f.Generated, Candidates: f.CandidateCount, Mutants: f.Mutants,
		Killed: f.Killed, Discarded: f.Discarded,
		Operators: f.Operators, Layer: layer, LayerReason: layerReason,
	}
	for _, s := range f.Open() {
		row.Open = append(row.Open, survivorRowPayload{Position: s.Position, Operator: s.Operator, Execution: s.Execution})
	}
	return row
}
