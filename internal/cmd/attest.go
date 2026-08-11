package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type attestOptions struct{ dir, findingsFile, symbol, position, operator, reason string }

func newAttestCommand() *cobra.Command {
	o := attestOptions{}
	cmd := &cobra.Command{Use: "attest", Short: "Attest an equivalent surviving mutant", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return attestCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to update")
	f.StringVar(&o.symbol, "symbol", "", "the mutated symbol")
	f.StringVar(&o.position, "position", "", "the survivor's position (file:line:col)")
	f.StringVar(&o.operator, "operator", "", "the survivor's operator")
	f.StringVar(&o.reason, "reason", "", "why the mutant is equivalent")
	return cmd
}

func attestCommand(ctx context.Context, o attestOptions, out io.Writer) error {
	if o.symbol == "" || o.position == "" || o.operator == "" || o.reason == "" {
		return fmt.Errorf("attest needs --symbol, --position, --operator, and --reason")
	}
	store, err := gomutant.OpenStore(findingsAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	var attested gomutant.Finding
	if err := store.Update(ctx, func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == o.symbol {
				if err := all[i].Attest(o.position, o.operator, o.reason); err != nil {
					return nil, err
				}
				attested = all[i]
				return all, nil
			}
		}
		return nil, fmt.Errorf("no finding for %s", o.symbol)
	}); err != nil {
		return err
	}
	// The echo states what the disposition did and where the record
	// lives; a record that cannot serve as it stands says so, because
	// the next measure judges the equivalence afresh and sheds the
	// disposition if its pins moved (REQ-attest-survivor).
	layer, layerReason := store.Layer(attested)
	switch layer {
	case "repo":
		fmt.Fprintf(out, "attested %s %s; %d open; layer: repo\n", o.position, o.operator, len(attested.Open()))
	default:
		fmt.Fprintf(out, "attested %s %s; %d open; layer: machine-local (%s)\n", o.position, o.operator, len(attested.Open()), layerReason)
	}
	tree, err := gomutant.LoadContext(ctx, o.dir)
	if err != nil {
		fmt.Fprintf(out, "warning: record state unavailable: %s\n", err)
		return nil
	}
	inspection, err := tree.InspectFindingContext(ctx, attested)
	if err != nil {
		fmt.Fprintf(out, "warning: record state unavailable: %s\n", err)
		return nil
	}
	if inspection.State != gomutant.FindingCurrent {
		fmt.Fprintf(out, "warning: the record is %s (%s) - the disposition is judged afresh when %s is re-measured\n", inspection.State, inspection.Reason, o.symbol)
	}
	return nil
}
