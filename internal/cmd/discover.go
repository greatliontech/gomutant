package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/spf13/cobra"
)

type discoverOptions struct {
	dir, changed, targetsFile string
	packages, symbols         []string
	json                      bool
}

type discoveryView struct {
	Targets []gomutant.TargetDescription `json:"targets"`
	Residue []gomutant.Residue           `json:"residue"`
}

func newDiscoverCommand() *cobra.Command {
	o := discoverOptions{}
	cmd := &cobra.Command{Use: "discover", Short: "Inspect effective mutation targets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return discoverCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.StringVar(&o.changed, "changed", "", "inspect symbols whose bodies differ from this git ref")
	f.StringVar(&o.targetsFile, "targets", "", "JSON targets document; overrides discovery")
	f.BoolVar(&o.json, "json", false, "render deterministic machine-readable targets")
	f.StringArrayVar(&o.packages, "package", nil, "package import-path glob; repeatable")
	f.StringArrayVar(&o.symbols, "symbol", nil, "fully qualified symbol glob; repeatable")
	return cmd
}

func discoverCommand(ctx context.Context, o discoverOptions) error {
	view, err := discoverTargets(ctx, o)
	if err != nil {
		return err
	}
	if o.json {
		return json.NewEncoder(os.Stdout).Encode(view)
	}
	if len(view.Targets) == 0 {
		fmt.Println("no targets")
	}
	for _, target := range view.Targets {
		mode := "derived"
		if target.OracleExplicit {
			mode = "explicit"
		}
		fmt.Printf("%s\n", target.Symbol)
		fmt.Printf("  oracle (%s): %s\n", mode, strings.Join(target.Oracle, ", "))
		if target.Skipped != "" {
			fmt.Printf("  skipped: %s\n", target.Skipped)
		}
		if len(target.Labels) != 0 {
			fmt.Printf("  labels: %s\n", strings.Join(target.Labels, ", "))
		}
	}
	for _, residue := range view.Residue {
		fmt.Printf("changed, untargeted  %s  (%s)\n", residue.Path, residue.Reason)
	}
	return nil
}

func discoverTargets(ctx context.Context, o discoverOptions) (discoveryView, error) {
	view := discoveryView{Targets: []gomutant.TargetDescription{}, Residue: []gomutant.Residue{}}
	tree, err := gomutant.LoadContext(ctx, o.dir)
	if err != nil {
		return view, err
	}
	var targets []gomutant.Target
	switch {
	case o.targetsFile != "" && o.changed != "":
		return view, fmt.Errorf("give --targets or --changed, not both")
	case o.targetsFile != "":
		data, err := contextio.ReadFile(ctx, o.targetsFile)
		if err != nil {
			return view, err
		}
		if err := ctx.Err(); err != nil {
			return view, err
		}
		targets, err = gomutant.LoadTargetsContext(ctx, data)
		if err != nil {
			return view, err
		}
	case o.changed == "":
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return view, err
		}
	default:
		paths, err := gitref.ChangedPathsContext(ctx, o.dir, o.changed)
		if err != nil {
			return view, err
		}
		targets, view.Residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, o.dir, o.changed, p)
		})
		if err != nil {
			return view, err
		}
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, o.packages, o.symbols)
	if err != nil {
		return view, err
	}
	view.Targets, err = tree.DescribeTargetsContext(ctx, targets)
	return view, err
}
