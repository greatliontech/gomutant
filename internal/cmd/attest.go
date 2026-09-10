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
	vouches                                               []string
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
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable); the posture is judged under the same acceptances the run used")
	return cmd
}

func attestCommand(ctx context.Context, o attestOptions, out io.Writer) error {
	if o.symbol == "" || o.position == "" || o.operator == "" || o.reason == "" {
		return fmt.Errorf("attest needs --symbol, --position, --operator, and --reason")
	}
	// The reasoning's shape is a declaration's: refused here, before
	// the document lock persists anything (REQ-exec-preparation); the
	// disposition seam keeps its own guard.
	if err := gomutant.ValidateAttestationReason(o.reason); err != nil {
		return err
	}
	// A vouch's shape is decidable from the flag alone: refused before
	// the write, as every declaration's shape is (REQ-exec-preparation).
	vouches, err := gomutant.ParseDynamicStateVouches(o.vouches)
	if err != nil {
		return err
	}
	// Provenance BEFORE the write: attest mutates the findings
	// document first, and a skewed binary must refuse outright rather
	// than write, echo success, and then fail (REQ-exec-provenance).
	if err := gomutant.CheckToolchainProvenance(ctx, o.dir, selectionOf(o.tags, o.toolchain)); err != nil {
		return err
	}
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	var attested gomutant.Finding
	if err := store.Update(ctx, func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		var err error
		all, attested, err = gomutant.AttestFinding(all, o.symbol, o.position, o.operator, o.reason)
		return all, err
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
	posture := attestedPosture(ctx, o.dir, selectionOf(o.tags, o.toolchain), vouches, attested)
	layerText := "repo"
	if layer != "repo" {
		layerText = "machine-local (" + layerReason + ")"
	}
	fmt.Fprintf(out, "attested %s %s; %d open; layer: %s; reuse: %s\n", o.position, o.operator, len(attested.Open()), layerText, posture.Line())
	return nil
}

// attestedPosture judges the attested record once under the call's
// selection; a tree or judgment fault is the posture's own reason.
func attestedPosture(ctx context.Context, dir string, sel gomutant.Selection, vouches []string, attested gomutant.Finding) gomutant.RecordPosture {
	tree, err := gomutant.LoadContextSelection(ctx, dir, sel)
	if err != nil {
		return gomutant.RecordedPosture(attested, gomutant.FindingInspection{}, err)
	}
	return judgeAttestedPosture(ctx, tree, vouches, attested)
}

// judgeAttestedPosture judges the record on a loaded tree under the
// same acceptances the run judged under — the MCP face's server-wide
// vouches give it those; the posture must not differ by face.
func judgeAttestedPosture(ctx context.Context, tree *gomutant.Tree, vouches []string, attested gomutant.Finding) gomutant.RecordPosture {
	tree.SetDynamicStateVouches(vouches...)
	inspection, err := tree.InspectFinding(ctx, attested)
	return gomutant.RecordedPosture(attested, inspection, err)
}
