package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type pruneOptions struct {
	dir, findingsFile string
	check             bool
	tags              []string
	toolchain         string
}

func newPruneCommand() *cobra.Command {
	o := pruneOptions{}
	cmd := &cobra.Command{Use: "prune", Short: guidanceShort("prune"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return pruneCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "")
	f.BoolVar(&o.check, "check", false, "")
	selectionFlags(f, &o.tags, &o.toolchain)
	return knobbedFlags(cmd, "prune")
}

func pruneCommand(ctx context.Context, o pruneOptions, out io.Writer) error {
	// The cadence starts before the store: its open is preparation the
	// line names (REQ-exec-run-status).
	out = &syncWriter{w: out}
	rep := newRunReporter(out, false, 0)
	defer rep.stop()
	rep.phase(gomutant.StretchPreparation)
	rep.startCadence(seams.progressInterval)
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	result, err := tree.PruneDetachedContext(ctx, store, o.check)
	if err != nil {
		return err
	}
	rep.epilogue(func(w io.Writer) { renderPrune(w, result) })
	return nil
}

func renderPrune(w io.Writer, result gomutant.PruneResult) {
	verb := "pruned"
	if result.Check {
		verb = "would prune"
	}
	for _, record := range result.Removed {
		fmt.Fprintf(w, "%s     %s%s\n", verb, record.Symbol, layerMarker(record.Layer))
		// The dispositions echo so the reasoning survives the removal -
		// promote-then-delete, never a silent drop
		// (REQ-result-lifecycle).
		for _, attestation := range record.Attested {
			fmt.Fprintf(w, "          attested %s %s  (%s)\n", attestation.Position, attestation.Operator, attestation.Reason)
		}
	}
	fmt.Fprintf(w, "%s %d record(s), %d kept (%d repo, %d machine-local)\n", verb, len(result.Removed), result.Kept.Total(), result.Kept.Repo, result.Kept.Local)
	if line := gomutant.RekeyedExemptionsLine(result.ExemptionsRekeyed); line != "" {
		fmt.Fprintln(w, line)
	}
}

// rewrittenExemptionRoster bounds the rewritten exemption subjects a
// retarget lists on this face: the first twenty, the remainder counted.
const rewrittenExemptionRoster = 20

type retargetOptions struct {
	dir, findingsFile, from, to string
	check                       bool
	tags                        []string
	toolchain                   string
}

func newRetargetCommand() *cobra.Command {
	o := retargetOptions{}
	cmd := &cobra.Command{Use: "retarget", Short: guidanceShort("retarget"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return retargetCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "")
	f.StringVar(&o.from, "from", "", "")
	f.StringVar(&o.to, "to", "", "")
	f.BoolVar(&o.check, "check", false, "")
	selectionFlags(f, &o.tags, &o.toolchain)
	return knobbedFlags(cmd, "retarget")
}

func retargetCommand(ctx context.Context, o retargetOptions, out io.Writer) error {
	// The pair's shape is two strings' business: refused before the
	// store and the load (REQ-exec-preparation).
	if err := gomutant.ValidateRetargetPair(o.from, o.to); err != nil {
		return err
	}
	// The cadence starts before the store: its open is preparation the
	// line names (REQ-exec-run-status).
	out = &syncWriter{w: out}
	rep := newRunReporter(out, false, 0)
	defer rep.stop()
	rep.phase(gomutant.StretchPreparation)
	rep.startCadence(seams.progressInterval)
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	result, err := tree.RetargetContext(ctx, store, o.from, o.to, o.check)
	if err != nil {
		return err
	}
	rep.epilogue(func(w io.Writer) { renderRetarget(w, result) })
	return nil
}

func renderRetarget(w io.Writer, result gomutant.RetargetResult) {
	verb := "retargeted"
	if result.Check {
		verb = "would retarget"
	}
	for _, record := range result.Rewritten {
		shadowed := ""
		if record.Shadowed {
			shadowed = "  (a machine-local record holds the new symbol and shadows this row)"
		}
		fmt.Fprintf(w, "%s %s -> %s%s%s\n", verb, record.From, record.To, layerMarker(record.Layer), shadowed)
	}
	// The touched surface owes no resolution, so the preview is the one
	// audit point - each field rewrite is echoed (REQ-result-lifecycle).
	for _, move := range result.TouchedRewrites {
		fmt.Fprintf(w, "%s on %s%s: %s -> %s\n", verb, move.Record, layerMarker(move.Layer), move.From, move.To)
	}
	if n := len(result.Exemptions); n > 0 {
		shown := result.Exemptions
		more := ""
		if n > rewrittenExemptionRoster {
			shown = shown[:rewrittenExemptionRoster]
			more = fmt.Sprintf(" (+%d more)", n-rewrittenExemptionRoster)
		}
		var moves []string
		for _, e := range shown {
			moves = append(moves, e.From+" -> "+e.To)
		}
		fmt.Fprintf(w, "%s %d reviewed exemption subject(s) with the records: %s%s\n", verb, n, strings.Join(moves, ", "), more)
	}
	if result.Touched.Total() > 0 {
		fmt.Fprintf(w, "%s %d further record(s) whose oracle or killer identities carry the rename (%d repo, %d machine-local)\n", verb, result.Touched.Total(), result.Touched.Repo, result.Touched.Local)
	}
	fmt.Fprintf(w, "%s %d record(s) (%d repo, %d machine-local)\n", verb, len(result.Rewritten), result.RewrittenCounts.Repo, result.RewrittenCounts.Local)
	if line := gomutant.RekeyedExemptionsLine(result.ExemptionsRekeyed); line != "" {
		fmt.Fprintln(w, line)
	}
}
