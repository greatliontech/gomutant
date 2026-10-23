package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

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

type explainedSurvivorView struct {
	Position  string `json:"position"`
	Operator  string `json:"operator"`
	Execution string `json:"execution,omitempty"`
	Advice    string `json:"advice"`
}

type promotionGroupView struct {
	Reason  string   `json:"reason"`
	Count   int      `json:"count"`
	Symbols []string `json:"symbols"`
}

type explainView struct {
	Symbol          string                  `json:"symbol,omitempty"`
	Selection       string                  `json:"selection,omitempty"`
	State           gomutant.FindingState   `json:"state,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	Layer           string                  `json:"layer,omitempty"`
	LayerReasons    []string                `json:"layerReasons,omitempty"`
	Open            []explainedSurvivorView `json:"open,omitempty"`
	Attested        int                     `json:"attested,omitempty"`
	Withdrawn       int                     `json:"withdrawn,omitempty"`
	RepoCommittable *int                    `json:"repoCommittable,omitempty"`
	LocalOnly       *int                    `json:"localOnly,omitempty"`
	Promotion       []promotionGroupView    `json:"promotion,omitempty"`
	Note            string                  `json:"note,omitempty"`
	Document        string                  `json:"document,omitempty"`
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
	if o.symbol != "" {
		selKey := gomutant.SelectionKey(sel)
		hasSel := len(o.tags) > 0 || o.toolchain != ""
		var matches []gomutant.Finding
		for _, finding := range all {
			if finding.Symbol != o.symbol {
				continue
			}
			if hasSel && finding.Selection != selKey {
				continue
			}
			matches = append(matches, finding)
		}
		if len(matches) > 1 {
			for _, m := range matches {
				if m.Selection == selKey {
					matches = []gomutant.Finding{m}
					break
				}
			}
		}
		if len(matches) > 1 {
			return fmt.Errorf("explain: multiple finding records for %s across build selections; specify --tag or --toolchain", o.symbol)
		}
		if len(matches) == 0 {
			return fmt.Errorf("explain: no finding for %s; gomutant findings lists the recorded symbols", o.symbol)
		}
		finding := matches[0]
		loadSel := sel
		if !hasSel && finding.Selection != "" {
			loadSel = gomutant.SelectionOfKey(finding.Selection)
		}
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
		layer, layerReasons := store.LayerReasons(finding)
		view := explainView{
			Symbol:       finding.Symbol,
			Selection:    finding.Selection,
			State:        inspection.State,
			Reason:       inspection.Reason,
			Layer:        layer,
			LayerReasons: gomutant.RollUpMachineLocalInputs(layerReasons),
			Attested:     len(finding.AttestedDispositions()),
			Withdrawn:    len(finding.WithdrawnDispositions()),
			Document:     docPath,
		}
		for _, s := range finding.Open() {
			view.Open = append(view.Open, explainedSurvivorView{
				Position:  s.Position,
				Operator:  s.Operator,
				Execution: s.Execution,
				Advice:    gomutant.SurvivorAdvice(s.Execution),
			})
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
	repo, local := 0, 0
	groups := map[string][]string{}
	hasSel := len(o.tags) > 0 || o.toolchain != ""
	selKey := gomutant.SelectionKey(sel)
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !(gomutant.RecordFilter{Label: o.label, Selection: selKey, HasSelection: hasSel}).Admits(finding) {
			continue
		}
		layer, layerReasons := store.LayerReasons(finding)
		if layer == gomutant.LayerRepo {
			repo++
			continue
		}
		local++
		for _, reason := range gomutant.RollUpMachineLocalInputs(layerReasons) {
			groups[reason] = append(groups[reason], finding.RecordKey().String())
		}
	}
	view := explainView{RepoCommittable: &repo, LocalOnly: &local, Document: docPath}
	switch {
	case len(all) == 0:
		view.Note = "no findings recorded at " + docPath + " - run measures the tree first"
	case o.label != "" && repo+local == 0:
		view.Note = fmt.Sprintf("label %q matched none of %d recorded finding(s); drop it to triage the document", o.label, len(all))
	}
	reasons := make([]string, 0, len(groups))
	for reason := range groups {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if len(groups[reasons[i]]) != len(groups[reasons[j]]) {
			return len(groups[reasons[i]]) > len(groups[reasons[j]])
		}
		return reasons[i] < reasons[j]
	})
	for _, reason := range reasons {
		syms := groups[reason]
		sort.Strings(syms)
		view.Promotion = append(view.Promotion, promotionGroupView{
			Reason:  reason,
			Count:   len(syms),
			Symbols: syms,
		})
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
