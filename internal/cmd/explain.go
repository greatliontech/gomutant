package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type explainOptions struct {
	dir, findingsFile string
	symbol, label     string
	tags              []string
	toolchain         string
	vouches           []string
	json              bool
}

type explainView struct {
	Symbol          string                       `json:"symbol,omitempty"`
	Selection       string                       `json:"selection,omitempty"`
	State           gomutant.FindingState        `json:"state,omitempty"`
	Reason          string                       `json:"reason,omitempty"`
	Layer           string                       `json:"layer,omitempty"`
	LayerReasons    []string                     `json:"layerReasons,omitempty"`
	Open            []gomutant.ExplainedSurvivor `json:"open,omitempty"`
	Attested        int                          `json:"attested,omitempty"`
	Withdrawn       int                          `json:"withdrawn,omitempty"`
	RepoCommittable *int                         `json:"repoCommittable,omitempty"`
	LocalOnly       *int                         `json:"localOnly,omitempty"`
	Promotion       []gomutant.PromotionGroup    `json:"promotion,omitempty"`
	Note            string                       `json:"note,omitempty"`
	Document        string                       `json:"document,omitempty"`
}

func newExplainCommand() *cobra.Command {
	o := explainOptions{}
	cmd := &cobra.Command{
		Use:    "explain",
		Short:  "Why a record stands as it does: clause lists, survivor prescriptions, or promotion triage",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return explainCommand(cmd.Context(), o, os.Stdout)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document path")
	f.StringVar(&o.symbol, "symbol", "", "mutated symbol to explain; empty explains the document's promotion triage")
	f.StringVar(&o.label, "label", "", "with no symbol, restrict promotion triage to findings carrying this label")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable)")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable explanation")
	return cmd
}

func explainCommand(ctx context.Context, o explainOptions, out io.Writer) error {
	if o.symbol != "" && o.label != "" {
		return fmt.Errorf("explain: the label filter restricts the triage arm; pass --symbol or --label, not both")
	}
	var vouches []string
	if len(o.vouches) > 0 {
		var err error
		if vouches, err = gomutant.ParseDynamicStateVouches(o.vouches); err != nil {
			return err
		}
	}
	sel := gomutant.Selection{Tags: o.tags, Toolchain: o.toolchain}
	if err := sel.Validate(); err != nil {
		return err
	}
	docPath := gomutant.FindingsPathAt(o.dir, o.findingsFile)
	store, err := gomutant.OpenStore(docPath, o.dir)
	if err != nil {
		return err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return err
	}
	hasSel := len(o.tags) > 0 || o.toolchain != ""
	if o.symbol != "" {
		finding, matches := gomutant.SelectExplainFinding(all, o.symbol, sel, hasSel)
		if matches > 1 {
			return fmt.Errorf("explain: multiple finding records for %s across build selections; specify --tag or --toolchain", o.symbol)
		}
		if matches == 0 {
			return fmt.Errorf("explain: no finding for %s; gomutant findings lists the recorded symbols", o.symbol)
		}
		loadSel := gomutant.AttestedLoadSelection(sel, hasSel, finding)
		tree, err := gomutant.LoadContextSelection(ctx, o.dir, loadSel)
		if err != nil {
			return err
		}
		if len(vouches) > 0 {
			tree.SetDynamicStateVouches(vouches...)
		}
		inspection, err := tree.InspectFinding(ctx, finding, nil)
		if err != nil {
			return err
		}
		explained := store.ExplainSymbolFinding(finding, inspection)
		view := explainView{
			Symbol:       explained.Symbol,
			Selection:    explained.Selection,
			State:        explained.State,
			Reason:       explained.Reason,
			Layer:        explained.Layer,
			LayerReasons: explained.LayerReasons,
			Open:         explained.Open,
			Attested:     explained.Attested,
			Withdrawn:    explained.Withdrawn,
			Document:     docPath,
		}
		if o.json {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(view)
		}
		fmt.Fprintf(out, "symbol    %s\nstate     %s", view.Symbol, view.State)
		if view.Reason != "" {
			fmt.Fprintf(out, " (%s)", view.Reason)
		}
		fmt.Fprintf(out, "\nlayer     %s\n", view.Layer)
		for _, r := range view.LayerReasons {
			fmt.Fprintf(out, "  clause  %s\n", r)
		}
		for _, s := range view.Open {
			fmt.Fprintf(out, "  open    %s  %s  [%s] -> %s\n", s.Position, s.Operator, s.Execution, s.Advice)
		}
		return nil
	}
	triage, err := store.ExplainTriage(ctx, all, gomutant.RecordFilter{
		Label:        o.label,
		Selection:    gomutant.SelectionKey(sel),
		HasSelection: hasSel,
	}, docPath)
	if err != nil {
		return err
	}
	repo, local := triage.RepoCommittable, triage.LocalOnly
	view := explainView{
		RepoCommittable: &repo,
		LocalOnly:       &local,
		Promotion:       triage.Promotion,
		Note:            triage.Note,
		Document:        docPath,
	}
	if o.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	}
	fmt.Fprintf(out, "triage    %d repo-committable, %d machine-local only (%s)\n", repo, local, docPath)
	if view.Note != "" {
		fmt.Fprintf(out, "note      %s\n", view.Note)
	}
	for _, g := range view.Promotion {
		fmt.Fprintf(out, "  %d x %s\n", g.Count, g.Reason)
	}
	return nil
}
