package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/spf13/cobra"
)

type runOptions struct {
	dir, changed, targetsFile, findingsFile string
	packages, symbols                       []string
	budget, jobs                            int
	timeout                                 time.Duration
	force                                   bool
	output                                  io.Writer
}

func newRunCommand() *cobra.Command {
	o := runOptions{}
	cmd := &cobra.Command{Use: "run", Short: "Measure mutants and update findings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runCommand(cmd.Context(), o)
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
	f.StringArrayVar(&o.packages, "package", nil, "package import-path glob; repeatable")
	f.StringArrayVar(&o.symbols, "symbol", nil, "fully qualified symbol glob; repeatable")
	return cmd
}

func runCommand(ctx context.Context, o runOptions) error {
	out := o.output
	if out == nil {
		out = os.Stdout
	}
	renderPreparation(out, gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
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
	targets, err = tree.FilterTargets(targets, o.packages, o.symbols)
	if err != nil {
		return err
	}
	for _, r := range residue {
		fmt.Fprintf(out, "changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	wholeTree := o.targetsFile == "" && o.changed == "" && len(o.packages) == 0 && len(o.symbols) == 0
	docPath := findingsAt(o.dir, o.findingsFile)
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(out, "no targets")
		renderRunSummary(out, gomutant.RunSummary{})
		if wholeTree {
			return gomutant.UpdateDocumentContext(ctx, docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return gomutant.MergeWholeFindings(current, nil, nil), nil
			})
		}
		return nil
	}
	prior, err := loadFindings(docPath)
	if err != nil {
		return err
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		Budget: o.budget, Timeout: o.timeout, Jobs: o.jobs, Force: o.force, Prior: prior,
		Decision: func(decision gomutant.RunDecision) {
			renderRunDecision(out, decision)
		},
		Progress: func(event gomutant.PreparationEvent) {
			renderPreparation(out, event)
		},
	})
	if err != nil {
		return err
	}
	for _, f := range findings {
		switch {
		case f.Skipped != "":
			fmt.Fprintf(out, "skipped   %s  (%s)\n", f.Symbol, f.Skipped)
		case f.Cached:
			fmt.Fprintf(out, "cached    %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		default:
			fmt.Fprintf(out, "measured  %s  %d/%d killed, %d open\n", f.Symbol, f.Killed, f.Mutants, len(f.Open()))
		}
		for _, s := range f.Open() {
			fmt.Fprintf(out, "          survivor %s %s\n", s.Position, s.Operator)
		}
		for _, summary := range f.Operators {
			fmt.Fprintf(out, "          operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
	}
	summary := gomutant.SummarizeRun(findings)
	renderRunSummary(out, summary)
	return gomutant.UpdateDocumentContext(ctx, docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wholeTree {
			return gomutant.MergeWholeFindings(current, findings, targets), nil
		}
		return gomutant.MergeFindings(current, findings), nil
	})
}

func renderPreparation(w io.Writer, event gomutant.PreparationEvent) {
	switch event.Stage {
	case gomutant.PreparationLoading:
		fmt.Fprintln(w, "prepare   loading")
	case gomutant.PreparationBaseline:
		fmt.Fprintf(w, "prepare   %s %s %s\n", event.Stage, event.Symbol, event.Package)
	default:
		fmt.Fprintf(w, "prepare   %s %s\n", event.Stage, event.Symbol)
	}
}

func renderRunDecision(w io.Writer, decision gomutant.RunDecision) {
	switch {
	case decision.Action == "measure":
		fmt.Fprintf(w, "measure   %s  %d mutants (%s)\n", decision.Symbol, decision.Mutants, decision.Reason)
	case decision.Reason != "":
		fmt.Fprintf(w, "%-9s %s  (%s)\n", decision.Action, decision.Symbol, decision.Reason)
	default:
		fmt.Fprintf(w, "%-9s %s\n", decision.Action, decision.Symbol)
	}
}

func renderRunSummary(w io.Writer, summary gomutant.RunSummary) {
	fmt.Fprintf(w, "summary   %d targets: %d measured, %d cached, %d skipped; %d generated, %d killed, %d survived, %d discarded; %d attested, %d open\n",
		summary.Targets, summary.Measured, summary.Cached, summary.Skipped, summary.Generated, summary.Killed, summary.Survived, summary.Discarded, summary.Attested, summary.Open)
}
