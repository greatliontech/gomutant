package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type attestOptions struct {
	dir, findingsFile, symbol, position, operator, reason string
	tags                                                  []string
	toolchain                                             string
}

func newAttestCommand() *cobra.Command {
	o := attestOptions{}
	cmd := &cobra.Command{Use: "attest", Short: guidanceShort("attest"), Long: guidanceHelp("attest"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return attestCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	selectionFlags(f, &o.tags, &o.toolchain)
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
	// Provenance BEFORE the write: attest mutates the findings
	// document first, and a skewed binary must refuse outright rather
	// than write, echo success, and then fail (REQ-exec-provenance).
	if err := gomutant.CheckToolchainProvenance(ctx, o.dir, selectionOf(o.tags, o.toolchain)); err != nil {
		return err
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
	// disposition if its mutation domain moved (REQ-attest-survivor).
	// The verdict line carries the record's posture whole — its layer
	// and its reuse, every refusing channel named — judged once before
	// the line is written, so a disposition is never read as reusable
	// evidence by a reader who stopped at the echo
	// (REQ-result-run-posture).
	layer, layerReason := store.Layer(attested)
	posture := attestedPosture(ctx, o.dir, selectionOf(o.tags, o.toolchain), attested)
	layerText := "repo"
	if layer != "repo" {
		layerText = "machine-local (" + layerReason + ")"
	}
	fmt.Fprintf(out, "attested %s %s; %d open; layer: %s; reuse: %s\n", o.position, o.operator, len(attested.Open()), layerText, posture.Line())
	return nil
}

// attestedPosture judges the attested record once under the call's
// selection; a tree or judgment fault is the posture's own reason.
func attestedPosture(ctx context.Context, dir string, sel gomutant.Selection, attested gomutant.Finding) gomutant.RecordPosture {
	tree, err := gomutant.LoadContextSelection(ctx, dir, sel)
	if err != nil {
		return gomutant.RecordedPosture(attested, gomutant.FindingInspection{}, err)
	}
	inspection, err := tree.InspectFindingContext(ctx, attested)
	return gomutant.RecordedPosture(attested, inspection, err)
}
