package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	gomutant "github.com/greatliontech/gomutant"
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

	committed     int // findings whose incremental commit returned
	bankedKilled  int
	bankedOpen    int
	lastExec      gomutant.ExecutionEvent
	lastMode      map[string]string // symbol -> last confirmation mode rendered
	stopCadence   chan struct{}
	cadenceDone   chan struct{}
	cadenceClosed sync.Once
	writeErr      error // first structured-face write failure, surfaced at exit
}

func newRunReporter(out io.Writer, jsonl bool, selected int) *runReporter {
	return &runReporter{
		out: out, jsonl: jsonl, start: time.Now(), selected: selected,
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
	r.mu.Unlock()
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
		Elapsed: time.Since(r.start).Round(time.Second).String(),
	}
}

func (r *runReporter) progressLine() {
	p := r.progressSnapshot()
	r.line("progress", p, func(w io.Writer) {
		fmt.Fprintf(w, "progress  %d/%d targets committed (%d served, %d skipped), candidates %d/%d, %d killed, %d open, elapsed %s\n",
			p.TargetsDone, p.Selected, p.Served, p.Skipped,
			p.CandidatesDone, p.CandidatesTotal, p.Killed, p.Open, p.Elapsed)
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
		fmt.Fprintf(w, "banked    %s after %s: %d target(s) committed to the findings document this run (%d killed, %d open among them); selection was %d target(s) — %d served, %d skipped before exit; every committed target is kept (REQ-exec-cancellation), the rest re-measure on the next run\n",
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
	"load":    "loading package graphs (gofresh analysis)",
	"observe": "observing oracle runtime inputs (freshness evidence)",
	"runtime": "validating runtime-input evidence (oracle freshness)",
	"prove":   "proving oracle closure freshness (gofresh hash proof)",
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
