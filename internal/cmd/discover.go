package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/spf13/cobra"
)

type discoverOptions struct {
	dir, changed, targetsFile string
	packages, symbols         []string
	tags                      []string
	toolchain                 string
	json                      bool
	// output receives the rendering; nil means stdout.
	output io.Writer
}

type discoveryView struct {
	Targets []gomutant.TargetDescription `json:"targets"`
	Residue []gomutant.Residue           `json:"residue"`
}

func newDiscoverCommand() *cobra.Command {
	o := discoverOptions{}
	cmd := &cobra.Command{Use: "discover", Short: guidanceShort("discover"), Long: guidanceHelp("discover"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return discoverCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "")
	f.StringVar(&o.changed, "changed", "", "")
	f.StringVar(&o.targetsFile, "targets", "", "")
	f.BoolVar(&o.json, "json", false, "")
	f.StringArrayVar(&o.packages, "package", nil, "")
	f.StringArrayVar(&o.symbols, "symbol", nil, "")
	selectionFlags(f, &o.tags, &o.toolchain)
	return knobbedFlags(cmd, "discover")
}

func discoverCommand(ctx context.Context, o discoverOptions) error {
	out := o.output
	if out == nil {
		out = os.Stdout
	}
	if o.json {
		view, err := discoverTargets(ctx, o, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(view)
	}
	// The human face names the preparation, the load, and the selection
	// under the cadence, and its rows render through the epilogue so no
	// progress line trails them (REQ-exec-run-status).
	out = &syncWriter{w: out}
	rep := newRunReporter(out, false, 0)
	defer rep.stop()
	rep.phase(gomutant.StretchPreparation)
	rep.startCadence(seams.progressInterval)
	view, err := discoverTargets(ctx, o, rep)
	if err != nil {
		return err
	}
	rep.epilogue(func(w io.Writer) { renderDiscovery(w, view, o) })
	return nil
}

func renderDiscovery(out io.Writer, view discoveryView, o discoverOptions) {
	if len(view.Targets) == 0 {
		fmt.Fprintln(out, "no targets: "+gomutant.SelectionEmptiedNote(o.targetsFile != "", o.changed, "--changed"))
	}
	for _, target := range view.Targets {
		mode := "derived"
		if target.OracleExplicit {
			mode = "explicit"
		}
		fmt.Fprintf(out, "%s\n", target.Symbol)
		fmt.Fprintf(out, "  oracle (%s): %s\n", mode, strings.Join(target.Oracle, ", "))
		if target.Skipped != "" {
			fmt.Fprintf(out, "  skipped: %s\n", target.Skipped)
		}
		if len(target.Labels) != 0 {
			fmt.Fprintf(out, "  labels: %s\n", strings.Join(target.Labels, ", "))
		}
	}
	for _, residue := range view.Residue {
		fmt.Fprintf(out, "changed, untargeted  %s  (%s)\n", residue.Path, residue.Reason)
	}
}

// discoverTargets resolves the effective targets; a non-nil reporter
// names the load's event and the selection's stretch as they begin.
func discoverTargets(ctx context.Context, o discoverOptions, rep *runReporter) (discoveryView, error) {
	view := discoveryView{Targets: []gomutant.TargetDescription{}, Residue: []gomutant.Residue{}}
	sources := gomutant.TargetSourcesGiven(gomutant.TargetSource{Name: "--targets", Given: o.targetsFile != ""}, gomutant.TargetSource{Name: "--changed", Given: o.changed != ""})
	request, err := gomutant.PrepareSelection(ctx, o.dir, sources, targetInputs(o.dir, o.targetsFile, o.changed), selectionOf(o.tags, o.toolchain), o.packages, o.symbols)
	if err != nil {
		return view, err
	}
	if rep != nil {
		rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	}
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return view, err
	}
	if rep != nil {
		rep.phase(gomutant.StretchSelecting)
	}
	selected, err := tree.SelectTargets(ctx, request)
	if err != nil {
		return view, err
	}
	targets := selected.Targets
	// The JSON face's residue is a list, empty or not.
	view.Residue = append(view.Residue, selected.Residue...)
	view.Targets, err = tree.DescribeTargets(ctx, targets)
	return view, err
}
