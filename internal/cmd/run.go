package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/spf13/cobra"
)

type runOptions struct {
	dir, changed, targetsFile, findingsFile string
	packages, symbols                       []string
	budget, jobs                            int
	timeout, oracleTimeout                  time.Duration
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
	f.IntVar(&o.budget, "budget", 0, "candidates per symbol; 0 = exhaustive")
	f.DurationVar(&o.timeout, "timeout", 0, "cancel command work before result commit after this duration; 0 = unlimited")
	f.DurationVar(&o.oracleTimeout, "oracle-timeout", 60*time.Second, "maximum duration of each oracle process")
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
	if o.timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if o.oracleTimeout < 0 {
		return fmt.Errorf("oracle timeout must not be negative")
	}
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}
	out := o.output
	if out == nil {
		out = os.Stdout
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	renderPreparation(out, gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContext(ctx, o.dir)
	if err != nil {
		return err
	}
	var targets []gomutant.Target
	var residue []gomutant.Residue
	switch {
	case o.targetsFile != "":
		data, err := contextio.ReadFile(ctx, o.targetsFile)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if targets, err = gomutant.LoadTargetsContext(ctx, data); err != nil {
			return err
		}
	case o.changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, o.dir, o.changed)
		if err != nil {
			return err
		}
		targets, residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, o.dir, o.changed, p)
		})
		if err != nil {
			return err
		}
	default:
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return err
		}
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, o.packages, o.symbols)
	if err != nil {
		return err
	}
	var terminal bytes.Buffer
	for _, r := range residue {
		fmt.Fprintf(&terminal, "changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	wholeTree := o.targetsFile == "" && o.changed == "" && len(o.packages) == 0 && len(o.symbols) == 0
	docPath := findingsAt(o.dir, o.findingsFile)
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(&terminal, "no targets")
		renderRunSummary(&terminal, gomutant.RunSummary{})
		if wholeTree {
			if err := gomutant.UpdateDocumentContext(ctx, docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return gomutant.MergeWholeFindings(current, nil, nil), nil
			}); err != nil {
				return err
			}
		}
		_, err := io.Copy(out, &terminal)
		return err
	}
	prior, err := loadFindingsContext(ctx, docPath)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		Budget: o.budget, OracleTimeout: o.oracleTimeout, Jobs: o.jobs, Force: o.force, Prior: prior,
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
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case f.Skipped != "":
			fmt.Fprintf(&terminal, "skipped   %s  (%s)\n", f.Symbol, f.Skipped)
		case f.Cached:
			fmt.Fprintf(&terminal, "cached    %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()))
		default:
			fmt.Fprintf(&terminal, "measured  %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()))
		}
		for _, s := range f.Open() {
			fmt.Fprintf(&terminal, "          survivor %s %s\n", s.Position, s.Operator)
		}
		for _, summary := range f.Operators {
			fmt.Fprintf(&terminal, "          operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
	}
	summary := gomutant.SummarizeRun(findings)
	renderRunSummary(&terminal, summary)
	if err := gomutant.UpdateDocumentContext(ctx, docPath, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wholeTree {
			return gomutant.MergeWholeFindings(current, findings, targets), nil
		}
		return gomutant.MergeFindings(current, findings), nil
	}); err != nil {
		return err
	}
	_, err = io.Copy(out, &terminal)
	return err
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
		fmt.Fprintf(w, "measure   %s  %d candidates (%s)\n", decision.Symbol, decision.Candidates, decision.Reason)
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
