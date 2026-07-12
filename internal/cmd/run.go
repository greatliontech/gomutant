package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/spf13/cobra"
)

type runOptions struct {
	dir, changed, targetsFile, findingsFile string
	budget, jobs                            int
	timeout                                 time.Duration
	force                                   bool
}

func newRunCommand() *cobra.Command {
	o := runOptions{}
	cmd := &cobra.Command{Use: "run", Short: "Measure mutants and update findings", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return runCommand(o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.IntVar(&o.budget, "budget", 0, "mutants per symbol; 0 = exhaustive")
	f.DurationVar(&o.timeout, "timeout", 60*time.Second, "one mutant's oracle run budget")
	f.IntVar(&o.jobs, "jobs", 0, "concurrent mutant runs; 0 = half the CPUs")
	f.BoolVar(&o.force, "force", false, "re-measure targets whose prior finding still covers")
	f.StringVar(&o.changed, "changed", "", "target only symbols whose bodies differ from this git ref")
	f.StringVar(&o.targetsFile, "targets", "", "JSON targets document; overrides discovery")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read and update")
	return cmd
}

func runCommand(o runOptions) error {
	tree, err := gomutant.Load(o.dir)
	if err != nil {
		return err
	}
	var targets []gomutant.Target
	var residue []gomutant.Residue
	switch {
	case o.targetsFile != "":
		data, err := os.ReadFile(o.targetsFile)
		if err != nil {
			return err
		}
		if targets, err = gomutant.LoadTargets(data); err != nil {
			return err
		}
	case o.changed != "":
		paths, err := gitref.ChangedPaths(o.dir, o.changed)
		if err != nil {
			return err
		}
		targets, residue = tree.DiscoverChanged(paths, func(p string) ([]byte, bool) {
			return gitref.Show(o.dir, o.changed, p)
		})
	default:
		targets = tree.Discover()
	}
	for _, r := range residue {
		fmt.Printf("changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	wholeTree := o.targetsFile == "" && o.changed == ""
	docPath := findingsAt(o.dir, o.findingsFile)
	if len(targets) == 0 {
		fmt.Println("no targets")
		if wholeTree {
			return gomutant.UpdateDocument(docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				return gomutant.MergeWholeFindings(current, nil, nil), nil
			})
		}
		return nil
	}
	prior, err := loadFindings(docPath)
	if err != nil {
		return err
	}
	findings, err := tree.Run(context.Background(), targets, gomutant.Options{
		Budget: o.budget, Timeout: o.timeout, Jobs: o.jobs, Force: o.force, Prior: prior,
	})
	if err != nil {
		return err
	}
	for _, f := range findings {
		switch {
		case f.Skipped != "":
			fmt.Printf("skipped   %s  (%s)\n", f.Symbol, f.Skipped)
		case f.Cached:
			fmt.Printf("cached    %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		default:
			fmt.Printf("measured  %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		}
		for _, s := range f.Open() {
			fmt.Printf("          survivor %s %s\n", s.Position, s.Operator)
		}
		for _, summary := range f.Operators {
			fmt.Printf("          operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
	}
	return gomutant.UpdateDocument(docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if wholeTree {
			return gomutant.MergeWholeFindings(current, findings, targets), nil
		}
		return gomutant.MergeFindings(current, findings), nil
	})
}
