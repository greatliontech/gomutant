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
	dir, findingsFile, label string
	json                     bool
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
	cmd := &cobra.Command{Use: "findings", Short: "List open mutation findings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return findingsCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read")
	f.StringVar(&o.label, "label", "", "show only findings carrying this label")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable findings")
	return cmd
}

func findingsCommand(ctx context.Context, o findingsOptions) error {
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
			return renderFindingsJSON(os.Stdout, []findingView{})
		}
		fmt.Println("no findings")
		return nil
	}
	tree, err := gomutant.LoadContext(ctx, o.dir)
	if err != nil {
		return err
	}
	views, err := inspectFindings(ctx, tree, store, all, o.label)
	if err != nil {
		return err
	}
	if o.json {
		return renderFindingsJSON(os.Stdout, views)
	}
	if len(views) == 0 {
		fmt.Println("no findings")
		return nil
	}
	renderFindingViews(os.Stdout, views)
	return nil
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

func inspectFindings(ctx context.Context, tree *gomutant.Tree, store *gomutant.Store, all []gomutant.Finding, label string) ([]findingView, error) {
	views := make([]findingView, 0, len(all))
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if label != "" && !contains(finding.Labels, label) {
			continue
		}
		inspection, err := tree.InspectFindingContext(ctx, finding)
		if err != nil {
			return nil, err
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
