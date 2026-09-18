package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type findingsOptions struct {
	judge                    bool
	dir, findingsFile, label string
	state, symbol            string
	detail, json             bool
	vouches                  []string
	tags                     []string
	toolchain                string
	// errOut carries the human notes the JSON face must keep out of its
	// document (the preserved legacy overlay line); nil discards them.
	errOut io.Writer
	// run scopes the roster to the records one campaign last measured.
	run string
	// changed cuts every rendered record's open survivors by this git
	// ref's added lines; loads the tree, derives no freshness.
	changed string
}

// judged reports whether any judged-question input was given: the
// state filter, a selection (tags/toolchain), or a vouch exist only
// to shape the freshness derivation, so any of them silently
// no-oping on the recorded path would be an inert flag.
func (o findingsOptions) judged() bool {
	return o.judge || o.state != "" || len(o.tags) > 0 || o.toolchain != "" || len(o.vouches) > 0
}

type findingView struct {
	Symbol      string                `json:"symbol"`
	Labels      []string              `json:"labels,omitempty"`
	State       gomutant.FindingState `json:"state"`
	Reason      string                `json:"reason,omitempty"`
	Layer       string                `json:"layer"`
	LayerReason string                `json:"layerReason,omitempty"`
	Run         string                `json:"run,omitempty"`
	// DeltaOpen is present exactly when a changed-ref cut ran (an
	// empty list is "cut ran, none on the delta").
	DeltaOpen      *[]gomutant.Survivor         `json:"deltaOpen,omitempty"`
	CandidateCount int                          `json:"candidateCount"`
	Generated      int                          `json:"generated"`
	Mutants        int                          `json:"mutants"`
	Killed         int                          `json:"killed"`
	Discarded      int                          `json:"discarded"`
	Operators      []gomutant.OperatorSummary   `json:"operators"`
	Open           []gomutant.Survivor          `json:"open"`
	Attested       []gomutant.Attestation       `json:"attested"`
	Candidates     []gomutant.CandidateEvidence `json:"candidateEvidence,omitempty"`
	// split marks the on-delta survivors for the human detail face.
	split gomutant.DeltaSurvivors
}

func newFindingsCommand() *cobra.Command {
	o := findingsOptions{}
	cmd := &cobra.Command{Use: "findings", Short: guidanceShort("findings"), Long: guidanceHelp("findings"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o.errOut = os.Stderr
		return findingsCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read")
	f.StringVar(&o.label, "label", "", "show only findings carrying this label")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.StringVar(&o.state, "state", "", "show only findings in this judged state: current, stale, unverifiable, or detached (implies --judge)")
	f.BoolVar(&o.judge, "judge", false, "re-derive each record's freshness state against the current tree - minutes-class on large documents; the default reports recorded facts with state 'recorded'")
	f.StringVar(&o.symbol, "symbol", "", "show only the finding for this mutated symbol")
	f.StringVar(&o.changed, "changed", "", "cut every record's open survivors against this git ref's delta: survivors on lines added since the ref are listed and counted distinctly (loads the tree; no freshness derived)")
	f.StringVar(&o.run, "run", "", "show only the records this run last measured (the identity a run prints first and stamps on every record it measures)")
	f.BoolVar(&o.detail, "detail", false, "full rows - operator tables, survivors, dispositions, candidate evidence; the default is one summary row per record")
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable); inspection judges under the same acceptances the run used (implies --judge)")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable findings")
	return cmd
}

func findingsCommand(ctx context.Context, o findingsOptions, out io.Writer) error {
	// The judge's cadence goroutine shares the writer with the rows.
	out = &syncWriter{w: out}
	// The inputs decide their refusals before the store opens: the
	// state's spelling and the vouches' (REQ-exec-preparation).
	if err := gomutant.ValidateFindingState(o.state); err != nil {
		return err
	}
	var vouches []string
	if len(o.vouches) > 0 {
		var err error
		if vouches, err = gomutant.ParseDynamicStateVouches(o.vouches); err != nil {
			return err
		}
	}
	// A changed ref's surface is read at preparation, in the tree root,
	// before any record is read — the one order every verb keeps
	// (REQ-exec-preparation); the cut resolves after the load.
	request, err := gomutant.PrepareSelection(ctx, o.dir, nil, targetInputs(o.dir, "", o.changed), selectionOf(o.tags, o.toolchain), nil, nil)
	if err != nil {
		return err
	}
	document := gomutant.FindingsPathAt(o.dir, o.findingsFile)
	filter := gomutant.RecordFilter{Label: o.label, Symbol: o.symbol, Run: o.run}
	store, err := gomutant.OpenStore(document, o.dir)
	if err != nil {
		return err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// What rides beside the rows is read with them, before any judging,
	// as the structured face reads it: the stated coverage bounds and
	// the committed ephemeral-equivalence record (REQ-result-unreached-bound,
	// REQ-result-ephemeral-attest).
	tail, err := readDocumentTail(ctx, store, document)
	if err != nil {
		return err
	}
	// Preserved legacy entries are named before any row on the human
	// face and beside the document on the JSON face, whose output is
	// the document itself (REQ-result-layers).
	if line := gomutant.LegacyOverlayLine(store.LegacyEntries()); line != "" {
		notes := out
		if o.json {
			notes = o.errOut
		}
		if notes != nil {
			fmt.Fprintln(notes, line)
		}
	}
	// Zero rows is an answer: after the tail — the attestation record
	// and the coverage bounds can exist without findings (the primary
	// loop shape persists no finding at all, and a wholly unreached leg
	// records a bound alone) — the note names the input that emptied
	// the roster, as the structured face does (REQ-result-inspection,
	// REQ-result-ephemeral-attest, REQ-result-unreached-bound).
	noRows := func(matched int) error {
		note := gomutant.NoRecordsNote(document, len(all), matched, filter, o.state)
		if o.json {
			// The JSON face is the rows alone; the note rides the human
			// channel beside it.
			if o.errOut != nil {
				fmt.Fprintln(o.errOut, note)
			}
			return renderFindingsJSON(out, []findingView{})
		}
		fmt.Fprintln(out, "no findings")
		renderDocumentTail(out, tail)
		fmt.Fprintln(out, note)
		return nil
	}
	if len(all) == 0 {
		return noRows(0)
	}
	matched := gomutant.FilterRecords(all, filter)
	if len(matched) == 0 {
		return noRows(0)
	}
	judge := o.judged()
	var tree *gomutant.Tree
	phase, stop := func(string) {}, func() {}
	// The changed-ref cut places survivor positions through the tree
	// without judging anything (REQ-result-inspection).
	if judge || o.changed != "" {
		// Judging derives freshness against the current tree — the
		// expensive truth. The default path loads no tree at all: the
		// document's recorded facts answer without one.
		// The human face carries the loading line and the cadence; the
		// JSON document is the machine face and stays a document.
		if !o.json {
			rep := newRunReporter(out, false, 0)
			defer rep.stop()
			stop = rep.stop
			rep.phase("loading")
			rep.startCadence(verbProgressInterval)
			rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
			phase = rep.phase
		}
		tree, err = gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
		if err != nil {
			return err
		}
		if len(vouches) > 0 {
			tree.SetDynamicStateVouches(vouches...)
		}
	}
	var cut *gomutant.DeltaCut
	if request.Changed != nil {
		selected, err := tree.SelectTargets(ctx, gomutant.SelectionRequest{Changed: request.Changed, Cut: true})
		if err != nil {
			return err
		}
		cut = selected.Cut
	}
	views, inspection, err := inspectFindings(ctx, tree, store, matched, findingFilters{state: o.state, judge: judge, cut: cut}, phase)
	// The rows render through the reporter's epilogue when one runs:
	// the cadence stops and joins before the first row.
	stop()
	if err != nil {
		return err
	}
	if len(views) == 0 {
		return noRows(len(matched))
	}
	if o.json {
		// The JSON face is the rows alone: the document on disk is the
		// machine face for what rides beside them — the coverage-bounds
		// table and the attestation record — exactly as the attestation
		// precedent below reads it.
		return renderFindingsJSON(out, views)
	}
	if !o.detail {
		renderFindingSummaries(out, views, inspection, judge, cut != nil)
	} else {
		renderFindingViews(out, views, inspection)
	}
	renderDocumentTail(out, tail)
	return nil
}

// readDocumentTail reads what rides the inspection beside the rows,
// for renderDocumentTail to print:
// the document's stated coverage bounds per declared selection
// (REQ-result-unreached-bound), then the committed
// ephemeral-equivalence record.
func readDocumentTail(ctx context.Context, store *gomutant.Store, document string) (documentTail, error) {
	var tail documentTail
	var err error
	if tail.bounds, err = store.CoverageBounds(ctx); err != nil {
		return tail, err
	}
	tail.attestations = gomutant.EphemeralAttestationsPathFor(document)
	atts, err := gomutant.LoadEphemeralAttestations(tail.attestations)
	if err != nil {
		return tail, err
	}
	tail.attested = len(atts)
	return tail, nil
}

// documentTail is what rides beside the rows, read with them.
type documentTail struct {
	bounds       []gomutant.CoverageBound
	attestations string
	attested     int
}

// renderDocumentTail prints the tail readDocumentTail read.
func renderDocumentTail(out io.Writer, tail documentTail) {
	for _, b := range tail.bounds {
		renderCoverageBound(out, b.Selection, b.Unreached)
	}
	if tail.attested > 0 {
		fmt.Fprintf(out, "%d ephemeral equivalence attestation%s on record — %s\n", tail.attested, plural(tail.attested), tail.attestations)
	}
}

// renderFindingSummaries is the bounded default: one row per record -
// state, symbol, layer, open and attested counts, the cause when the
// record cannot serve - with the full lists behind --detail
// (REQ-result-inspection).
func renderFindingSummaries(w io.Writer, views []findingView, inspection gomutant.Inspection, judged bool, cut bool) {
	for _, view := range views {
		layer := view.Layer
		if layer == "local" {
			layer = "machine-local"
		}
		fmt.Fprintf(w, "%s  %s  [%s]  %d open", view.State, view.Symbol, layer, len(view.Open))
		if cut && view.DeltaOpen != nil {
			fmt.Fprintf(w, " (%d on the delta)", len(*view.DeltaOpen))
		}
		fmt.Fprintf(w, ", %d attested", len(view.Attested))
		if view.Reason != "" {
			fmt.Fprintf(w, "  (%s)", view.Reason)
		}
		fmt.Fprintln(w, runSuffix(view.Run))
	}
	fmt.Fprintf(w, "%d repo-committable, %d machine-local; --detail for survivors and dispositions", inspection.Repo, inspection.Local)
	if !judged {
		// The recorded default must name the judged opt-in at the
		// point of use, or the freshness states are undiscoverable
		// (REQ-result-inspection).
		fmt.Fprint(w, "; --judge for freshness states")
	}
	fmt.Fprintln(w)
}

func renderFindingViews(w io.Writer, views []findingView, inspection gomutant.Inspection) {
	repoCount, localOnly := inspection.Repo, inspection.Local
	for _, view := range views {
		labels := view.Labels
		if len(labels) == 0 {
			labels = []string{"(unlabeled)"}
		}
		fmt.Fprintf(w, "%s\n", strings.Join(labels, ", "))
		fmt.Fprintf(w, "  %s  %s", view.State, view.Symbol)
		fmt.Fprintf(w, "  %d/%d candidates, %d mutants, %d killed, %d discarded; %d open",
			view.Generated, view.CandidateCount, view.Mutants, view.Killed, view.Discarded, len(view.Open))
		if view.DeltaOpen != nil {
			fmt.Fprintf(w, " (%d on the delta)", len(*view.DeltaOpen))
		}
		fmt.Fprintf(w, ", %d attested\n", len(view.Attested))
		// The cause leads: a record that cannot be reused says why before
		// anything it found (REQ-result-inspection).
		if view.Reason != "" {
			fmt.Fprintf(w, "    cause: %s\n", view.Reason)
		}
		for i, survivor := range view.Open {
			mark := ""
			if view.split.IsOnDelta(i) {
				mark = "  [delta]"
			}
			if survivor.Execution != "" {
				fmt.Fprintf(w, "    survivor %s %s  [%s]%s\n", survivor.Position, survivor.Operator, survivor.Execution, mark)
				continue
			}
			fmt.Fprintf(w, "    survivor %s %s%s\n", survivor.Position, survivor.Operator, mark)
		}
		for _, summary := range view.Operators {
			fmt.Fprintf(w, "    operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
		for _, attestation := range view.Attested {
			fmt.Fprintf(w, "    attested %s %s  (%s)\n", attestation.Position, attestation.Operator, attestation.Reason)
		}
		if view.Layer == "local" {
			fmt.Fprintf(w, "    machine-local: %s\n", view.LayerReason)
		}
		for _, candidate := range view.Candidates {
			fmt.Fprintf(w, "    unverifiable candidate %s %s  (%s)\n", candidate.Position, candidate.Operator, candidate.Reason)
		}
	}
	fmt.Fprintf(w, "%d repo-committable, %d machine-local\n", repoCount, localOnly)
}

func renderFindingsJSON(w io.Writer, views []findingView) error {
	return json.NewEncoder(w).Encode(views)
}

// findingFilters selects the records an inspection renders: the
// recorded-fact filter the library defines, plus the judged state
// applied after inspection (REQ-result-inspection).
type findingFilters struct {
	state string
	// judge derives each record's freshness against the tree; the
	// tree may be loaded for the cut alone, so presence never implies it.
	judge bool
	// cut, when set, splits each view's open survivors by the delta.
	cut *gomutant.DeltaCut
}

func inspectFindings(ctx context.Context, tree *gomutant.Tree, store *gomutant.Store, matched []gomutant.Finding, filters findingFilters, phase func(string)) ([]findingView, gomutant.Inspection, error) {
	inspection, err := gomutant.InspectDocument(ctx, tree, store, matched, gomutant.InspectionRequest{Judge: filters.judge, State: filters.state, Cut: filters.cut, Phase: phase})
	if err != nil {
		return nil, inspection, err
	}
	views := make([]findingView, 0, len(inspection.Rows))
	for _, row := range inspection.Rows {
		finding := row.Finding
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		var onDelta *[]gomutant.Survivor
		var split gomutant.DeltaSurvivors
		if row.Delta != nil {
			split = *row.Delta
			listed := append([]gomutant.Survivor{}, split.OnDelta...)
			onDelta = &listed
		}
		views = append(views, findingView{
			Symbol: finding.Symbol, Labels: labels, State: row.Inspection.State, Reason: row.Inspection.Reason,
			Layer: row.Layer, LayerReason: row.LayerReason, Run: finding.Run, DeltaOpen: onDelta, split: split,
			CandidateCount: finding.CandidateCount, Generated: finding.Generated,
			Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
			Candidates: row.Inspection.CandidateEvidence,
		})
	}
	return views, inspection, nil
}
