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
	Symbol    string                     `json:"symbol"`
	Labels    []string                   `json:"labels,omitempty"`
	State     gomutant.FindingState      `json:"state"`
	Reason    string                     `json:"reason,omitempty"`
	Operators []gomutant.OperatorSummary `json:"operators"`
	Open      []gomutant.Survivor        `json:"open"`
	Attested  []gomutant.Attestation     `json:"attested"`
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
	all, err := loadFindingsContext(ctx, findingsAt(o.dir, o.findingsFile))
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
	views, err := inspectFindings(ctx, tree, all, o.label)
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
	for _, view := range views {
		labels := view.Labels
		if len(labels) == 0 {
			labels = []string{"(unlabeled)"}
		}
		fmt.Printf("%s\n", strings.Join(labels, ", "))
		fmt.Printf("  %s  %s", view.State, view.Symbol)
		if view.Reason != "" {
			fmt.Printf("  (%s)", view.Reason)
		}
		fmt.Printf("  %d open, %d attested\n", len(view.Open), len(view.Attested))
		for _, survivor := range view.Open {
			fmt.Printf("    survivor %s %s\n", survivor.Position, survivor.Operator)
		}
		for _, summary := range view.Operators {
			fmt.Printf("    operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
		for _, attestation := range view.Attested {
			fmt.Printf("    attested %s %s  (%s)\n", attestation.Position, attestation.Operator, attestation.Reason)
		}
	}
	return nil
}

func renderFindingsJSON(w io.Writer, views []findingView) error {
	return json.NewEncoder(w).Encode(views)
}

func inspectFindings(ctx context.Context, tree *gomutant.Tree, all []gomutant.Finding, label string) ([]findingView, error) {
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
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		views = append(views, findingView{
			Symbol: finding.Symbol, Labels: labels, State: inspection.State, Reason: inspection.Reason,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
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
