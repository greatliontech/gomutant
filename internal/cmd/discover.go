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
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.changed, "changed", "", "inspect symbols whose bodies differ from this git ref; exclusive with --targets")
	f.StringVar(&o.targetsFile, "targets", "", "JSON targets document; overrides discovery, exclusive with --changed")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable targets")
	f.StringArrayVar(&o.packages, "package", nil, "package import-path glob; repeatable")
	f.StringArrayVar(&o.symbols, "symbol", nil, "fully qualified symbol glob; repeatable")
	selectionFlags(f, &o.tags, &o.toolchain)
	return cmd
}

func discoverCommand(ctx context.Context, o discoverOptions) error {
	view, err := discoverTargets(ctx, o)
	if err != nil {
		return err
	}
	out := o.output
	if out == nil {
		out = os.Stdout
	}
	if o.json {
		return json.NewEncoder(out).Encode(view)
	}
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
	return nil
}

func discoverTargets(ctx context.Context, o discoverOptions) (discoveryView, error) {
	view := discoveryView{Targets: []gomutant.TargetDescription{}, Residue: []gomutant.Residue{}}
	sources := gomutant.TargetSourcesGiven(gomutant.TargetSource{Name: "--targets", Given: o.targetsFile != ""}, gomutant.TargetSource{Name: "--changed", Given: o.changed != ""})
	request, err := gomutant.PrepareSelection(ctx, o.dir, sources, targetInputs(o.dir, o.targetsFile, o.changed), selectionOf(o.tags, o.toolchain), o.packages, o.symbols)
	if err != nil {
		return view, err
	}
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return view, err
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
