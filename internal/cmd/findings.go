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
}

// judged reports whether any judged-question input was given: the
// state filter, a selection (tags/toolchain), or a vouch exist only
// to shape the freshness derivation, so any of them silently
// no-oping on the recorded path would be an inert flag.
func (o findingsOptions) judged() bool {
	return o.judge || o.state != "" || len(o.tags) > 0 || o.toolchain != "" || len(o.vouches) > 0
}

type findingView struct {
	Symbol         string                       `json:"symbol"`
	Labels         []string                     `json:"labels,omitempty"`
	State          gomutant.FindingState        `json:"state"`
	Reason         string                       `json:"reason,omitempty"`
	Layer          string                       `json:"layer"`
	LayerReason    string                       `json:"layerReason,omitempty"`
	CandidateCount int                          `json:"candidateCount"`
	Generated      int                          `json:"generated"`
	Mutants        int                          `json:"mutants"`
	Killed         int                          `json:"killed"`
	Discarded      int                          `json:"discarded"`
	Operators      []gomutant.OperatorSummary   `json:"operators"`
	Open           []gomutant.Survivor          `json:"open"`
	Attested       []gomutant.Attestation       `json:"attested"`
	Candidates     []gomutant.CandidateEvidence `json:"candidateEvidence,omitempty"`
}

func newFindingsCommand() *cobra.Command {
	o := findingsOptions{}
	cmd := &cobra.Command{Use: "findings", Short: "Inspect the findings document: states, survivors, dispositions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
	f.BoolVar(&o.detail, "detail", false, "full rows - operator tables, survivors, dispositions, candidate evidence; the default is one summary row per record")
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable); inspection judges under the same acceptances the run used (implies --judge)")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable findings")
	return cmd
}

func findingsCommand(ctx context.Context, o findingsOptions, out io.Writer) error {
	switch o.state {
	case "", string(gomutant.FindingCurrent), string(gomutant.FindingStale), string(gomutant.FindingUnverifiable), string(gomutant.FindingDetached):
	default:
		return fmt.Errorf("unknown state %q (current, stale, unverifiable, detached)", o.state)
	}
	store, err := gomutant.OpenStore(findingsAt(o.dir, o.findingsFile), o.dir)
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
	if len(all) == 0 {
		if o.json {
			return renderFindingsJSON(out, []findingView{})
		}
		fmt.Fprintln(out, "no findings")
		return nil
	}
	judge := o.judged()
	var tree *gomutant.Tree
	if judge {
		// Judging derives freshness against the current tree — the
		// expensive truth. The default path loads no tree at all: the
		// document's recorded facts answer without one.
		tree, err = gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
		if err != nil {
			return err
		}
		if len(o.vouches) > 0 {
			identities, err := gomutant.ParseDynamicStateVouches(o.vouches)
			if err != nil {
				return err
			}
			tree.SetDynamicStateVouches(identities...)
		}
	}
	views, err := inspectFindings(ctx, tree, store, all, o.label, o.state, o.symbol)
	if err != nil {
		return err
	}
	if o.json {
		return renderFindingsJSON(out, views)
	}
	if len(views) == 0 {
		fmt.Fprintln(out, "no findings")
		return nil
	}
	if !o.detail {
		renderFindingSummaries(out, views, judge)
		return nil
	}
	renderFindingViews(out, views)
	return nil
}

// renderFindingSummaries is the bounded default: one row per record -
// state, symbol, layer, open and attested counts, the cause when the
// record cannot serve - with the full lists behind --detail
// (REQ-result-inspection).
func renderFindingSummaries(w io.Writer, views []findingView, judged bool) {
	repoCount, localOnly := 0, 0
	for _, view := range views {
		if view.Layer == "repo" {
			repoCount++
		} else {
			localOnly++
		}
		layer := view.Layer
		if layer == "local" {
			layer = "machine-local"
		}
		fmt.Fprintf(w, "%s  %s  [%s]  %d open, %d attested", view.State, view.Symbol, layer, len(view.Open), len(view.Attested))
		if view.Reason != "" {
			fmt.Fprintf(w, "  (%s)", view.Reason)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%d repo-committable, %d machine-local; --detail for survivors and dispositions", repoCount, localOnly)
	if !judged {
		// The recorded default must name the judged opt-in at the
		// point of use, or the freshness states are undiscoverable
		// (REQ-result-inspection).
		fmt.Fprint(w, "; --judge for freshness states")
	}
	fmt.Fprintln(w)
}

func renderFindingViews(w io.Writer, views []findingView) {
	repoCount, localOnly := 0, 0
	for _, view := range views {
		if view.Layer == "repo" {
			repoCount++
		} else {
			localOnly++
		}
	}
	for _, view := range views {
		labels := view.Labels
		if len(labels) == 0 {
			labels = []string{"(unlabeled)"}
		}
		fmt.Fprintf(w, "%s\n", strings.Join(labels, ", "))
		fmt.Fprintf(w, "  %s  %s", view.State, view.Symbol)
		fmt.Fprintf(w, "  %d/%d candidates, %d mutants, %d killed, %d discarded; %d open, %d attested\n",
			view.Generated, view.CandidateCount, view.Mutants, view.Killed, view.Discarded, len(view.Open), len(view.Attested))
		// The cause leads: a record that cannot be reused says why before
		// anything it found (REQ-result-inspection).
		if view.Reason != "" {
			fmt.Fprintf(w, "    cause: %s\n", view.Reason)
		}
		for _, survivor := range view.Open {
			if survivor.Execution != "" {
				fmt.Fprintf(w, "    survivor %s %s  [%s]\n", survivor.Position, survivor.Operator, survivor.Execution)
				continue
			}
			fmt.Fprintf(w, "    survivor %s %s\n", survivor.Position, survivor.Operator)
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

func inspectFindings(ctx context.Context, tree *gomutant.Tree, store *gomutant.Store, all []gomutant.Finding, label, state, symbol string) ([]findingView, error) {
	views := make([]findingView, 0, len(all))
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if label != "" && !contains(finding.Labels, label) {
			continue
		}
		if symbol != "" && finding.Symbol != symbol {
			continue
		}
		// A nil tree is the recorded path: no freshness derivation,
		// the record's own facts, state "recorded".
		inspection := gomutant.RecordedInspection(finding)
		if tree != nil {
			judged, err := tree.InspectFindingContext(ctx, finding)
			if err != nil {
				return nil, err
			}
			inspection = judged
		}
		if state != "" && string(inspection.State) != state {
			continue
		}
		layer, layerReason := store.Layer(finding)
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		views = append(views, findingView{
			Symbol: finding.Symbol, Labels: labels, State: inspection.State, Reason: inspection.Reason,
			Layer: layer, LayerReason: layerReason,
			CandidateCount: finding.CandidateCount, Generated: finding.Generated,
			Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
			Candidates: inspection.CandidateEvidence,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Symbol < views[j].Symbol })
	return views, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
