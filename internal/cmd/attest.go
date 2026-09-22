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
	f.StringVar(&o.dir, "dir", ".", "")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "")
	f.StringVar(&o.symbol, "symbol", "", "")
	f.StringVar(&o.position, "position", "", "")
	f.StringVar(&o.operator, "operator", "", "")
	f.StringVar(&o.reason, "reason", "", "")
	f.StringArrayVar(&o.vouches, "vouch", nil, "")
	return knobbedFlags(cmd, "attest")
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
	// The root's existence, then the standing vouch set's shape read in
	// it — the preparation order every verb keeps (REQ-exec-preparation).
	if err := gomutant.CheckTreeRoot(o.dir); err != nil {
		return err
	}
	if _, err := gomutant.StandingVouches(o.dir); err != nil {
		return err
	}
	// Provenance BEFORE the write: attest mutates the findings
	// document first, and a skewed binary must refuse outright rather
	// than write, echo success, and then fail (REQ-exec-provenance).
	if err := gomutant.CheckToolchainProvenance(ctx, o.dir, selectionOf(o.tags, o.toolchain)); err != nil {
		return err
	}
	// The cadence starts before the store and the document lock — a
	// lock held by a running campaign is a stretch the line names, not
	// a silence; the posture's load and judgment follow under it, and
	// the echo renders through the epilogue so no progress line trails
	// it (REQ-exec-run-status).
	out = &syncWriter{w: out}
	rep := newRunReporter(out, false, 0)
	defer rep.stop()
	rep.phase(gomutant.StretchPreparation)
	rep.startCadence(seams.progressInterval)
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
	posture := attestedPosture(ctx, o.dir, selectionOf(o.tags, o.toolchain), vouches, attested, rep)
	layerText := gomutant.LayerRepo
	if layer != gomutant.LayerRepo {
		layerText = "machine-local (" + layerReason + ")"
	}
	rep.epilogue(func(w io.Writer) {
		fmt.Fprintf(w, "attested %s %s; %d open; layer: %s; reuse: %s\n", o.position, o.operator, len(attested.Open()), layerText, posture.Line())
	})
	return nil
}

// attestedPosture judges the attested record once under the call's
// selection, the load and the judgment named on the reporter; a tree
// or judgment fault is the posture's own reason.
func attestedPosture(ctx context.Context, dir string, sel gomutant.Selection, vouches []string, attested gomutant.Finding, rep *runReporter) gomutant.RecordPosture {
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContextSelection(ctx, dir, sel)
	if err != nil {
		return gomutant.RecordedPosture(attested, gomutant.FindingInspection{}, err)
	}
	return judgeAttestedPosture(ctx, tree, vouches, attested, func(stage string) { rep.phase(gomutant.StretchInspecting(stage)) })
}

// judgeAttestedPosture judges the record on a loaded tree under the
// same acceptances the run judged under — the MCP face's server-wide
// vouches give it those; the posture must not differ by face. The
// walk's stages reach progress when it is non-nil.
func judgeAttestedPosture(ctx context.Context, tree *gomutant.Tree, vouches []string, attested gomutant.Finding, progress func(stage string)) gomutant.RecordPosture {
	tree.SetDynamicStateVouches(vouches...)
	inspection, err := tree.InspectFinding(ctx, attested, progress)
	return gomutant.RecordedPosture(attested, inspection, err)
}
