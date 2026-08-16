package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

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
	cmd := &cobra.Command{Use: "prune", Short: "Remove records whose mutated symbol no longer resolves", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return pruneCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to update")
	f.BoolVar(&o.check, "check", false, "preview the removals without touching the document")
	selectionFlags(f, &o.tags, &o.toolchain)
	return cmd
}

func pruneCommand(ctx context.Context, o pruneOptions, out io.Writer) error {
	store, err := gomutant.OpenStore(findingsAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	result, err := tree.PruneDetachedContext(ctx, store, o.check)
	if err != nil {
		return err
	}
	renderPrune(out, result)
	return nil
}

func renderPrune(w io.Writer, result gomutant.PruneResult) {
	verb := "pruned"
	if result.Check {
		verb = "would prune"
	}
	for _, record := range result.Removed {
		fmt.Fprintf(w, "%s     %s\n", verb, record.Symbol)
		// The dispositions echo so the reasoning survives the removal -
		// promote-then-delete, never a silent drop
		// (REQ-result-lifecycle).
		for _, attestation := range record.Attested {
			fmt.Fprintf(w, "          attested %s %s  (%s)\n", attestation.Position, attestation.Operator, attestation.Reason)
		}
	}
	fmt.Fprintf(w, "%s %d record(s), %d kept\n", verb, len(result.Removed), result.Kept)
}

type retargetOptions struct {
	dir, findingsFile, from, to string
	check                       bool
	tags                        []string
	toolchain                   string
}

func newRetargetCommand() *cobra.Command {
	o := retargetOptions{}
	cmd := &cobra.Command{Use: "retarget", Short: "Rewrite symbol identity across a rename; dispositions follow their mutants", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return retargetCommand(cmd.Context(), o, os.Stdout)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root the default document anchors at")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to update")
	f.StringVar(&o.from, "from", "", "old symbol prefix: a package pair renames a package (a dot-terminated pass covers its own symbols, a slash-terminated pass its subpackages); a symbol pair renames within its package, segment for segment")
	f.StringVar(&o.to, "to", "", "new symbol prefix, terminated like --from")
	f.BoolVar(&o.check, "check", false, "preview the rewrites without touching the document")
	selectionFlags(f, &o.tags, &o.toolchain)
	return cmd
}

func retargetCommand(ctx context.Context, o retargetOptions, out io.Writer) error {
	store, err := gomutant.OpenStore(findingsAt(o.dir, o.findingsFile), o.dir)
	if err != nil {
		return err
	}
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	result, err := tree.RetargetContext(ctx, store, o.from, o.to, o.check)
	if err != nil {
		return err
	}
	verb := "retargeted"
	if result.Check {
		verb = "would retarget"
	}
	for _, record := range result.Rewritten {
		fmt.Fprintf(out, "%s %s -> %s\n", verb, record.From, record.To)
	}
	// The touched surface owes no resolution, so the preview is the one
	// audit point - each field rewrite is echoed (REQ-result-lifecycle).
	for _, move := range result.TouchedRewrites {
		fmt.Fprintf(out, "%s on %s: %s -> %s\n", verb, move.Record, move.From, move.To)
	}
	if result.Touched > 0 {
		fmt.Fprintf(out, "%s %d further record(s) whose oracle or killer identities carry the rename\n", verb, result.Touched)
	}
	fmt.Fprintf(out, "%s %d record(s)\n", verb, len(result.Rewritten))
	return nil
}
