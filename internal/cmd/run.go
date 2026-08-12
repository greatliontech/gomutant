package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
	oracleMemoryMiB                         int64
	timeout, oracleTimeout                  time.Duration
	force, plan                             bool
	bracketPaths, vouches                   []string
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
	f.Int64Var(&o.oracleMemoryMiB, "oracle-memory-mib", 0, "memory ceiling per oracle process tree in MiB (GOMEMLIMIT plus a hard data-segment cap): 0 derives RAM/(2 x jobs) floored at 1 GiB, -1 disables; a runaway-allocation mutant dies on its own ceiling as an ordinary kill instead of OOMing the host")
	f.IntVar(&o.jobs, "jobs", 0, "concurrent mutant runs; 0 = half the CPUs")
	f.StringArrayVar(&o.bracketPaths, "bracket-path", nil, "external surface the oracle legitimately reads (module-relative path or absolute file, repeatable; absolute directories and tool-excluded paths are refused); extends each spawn's observation bracket, carrying the caller's assertion the surface is mutation-free for the run")
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, recorded on the evidence")
	f.BoolVar(&o.force, "force", false, "re-measure even targets whose prior finding still covers the request; the pin spans the mutated symbol's body, every oracle test's source closure, and the observed runtime inputs (toolchain, build configuration, and the other measurement pins are always compared too), so new or changed oracle tests re-measure without --force")
	f.StringVar(&o.changed, "changed", "", "target only symbols whose bodies differ from this git ref")
	f.StringVar(&o.targetsFile, "targets", "", "path to a JSON targets document (gomutant's or a producer's export); overrides discovery")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read and update")
	f.StringArrayVar(&o.packages, "package", nil, "package import-path glob; repeatable")
	f.StringArrayVar(&o.symbols, "symbol", nil, "fully qualified symbol glob; repeatable")
	f.BoolVar(&o.plan, "plan", false, "preflight only: run the full preparation sequence and print every target decision — cached, skipped with reason, or measure with candidate count — then stop before baseline probes and mutant execution, persisting nothing; precondition holes surface before any budget is spent")
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
	if len(o.vouches) > 0 {
		identities, err := gomutant.ParseDynamicStateVouches(o.vouches)
		if err != nil {
			return err
		}
		tree.SetDynamicStateVouches(identities...)
	}
	var targets []gomutant.Target
	var residue []gomutant.Residue
	switch {
	case o.targetsFile != "":
		data, err := contextio.ReadFile(ctx, o.targetsFile)
		if err != nil {
			if trimmed := strings.TrimSpace(o.targetsFile); strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				return fmt.Errorf("--targets expects a file path; the value looks like an inline JSON document - write it to a file first: %w", err)
			}
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
	wholeTree := o.targetsFile == "" && o.changed == "" && len(o.packages) == 0 && len(o.symbols) == 0
	docPath := findingsAt(o.dir, o.findingsFile)
	// The campaign lock spans measurement through the final merge:
	// a second campaign against the same document refuses immediately
	// instead of interleaving (REQ-exec-exclusivity).
	releaseCampaign, err := gomutant.AcquireCampaignLock(docPath)
	if err != nil {
		return err
	}
	defer releaseCampaign()
	docStore, err := gomutant.OpenStore(docPath, o.dir)
	if err != nil {
		return err
	}
	prior, err := loadFindingsContext(ctx, o.dir, docPath)
	if err != nil {
		return err
	}
	if residue, err = tree.OracleClosureSignpostContext(ctx, residue, prior, targets); err != nil {
		return err
	}
	var terminal bytes.Buffer
	for _, r := range residue {
		fmt.Fprintf(&terminal, "changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintln(&terminal, "no targets")
		if !o.plan {
			renderRunSummary(&terminal, gomutant.RunSummary{})
		}
		if o.plan {
			// The plan clause's no-write guarantee covers the empty
			// whole-tree reconciliation too: a plan never prunes.
			fmt.Fprintln(&terminal, "plan only: no baselines probed, no mutants executed, nothing persisted")
			_, err := io.Copy(out, &terminal)
			return err
		}
		if wholeTree {
			if err := docStore.Update(ctx, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	var planMeasure, planCandidates, planCached, planSkipped int
	var commitSheds []gomutant.AttestationShed
	// The run-start snapshot of dispositions per symbol makes the merge
	// graft pin-correct, and the post-merge rows are the rendering
	// truth: what the response describes is what the document holds
	// (REQ-attest-survivor, REQ-mcp-findings-doc).
	attestSnapshot := map[string][]gomutant.Attestation{}
	for _, f := range prior {
		attestSnapshot[f.Symbol] = append([]gomutant.Attestation(nil), f.Attested...)
	}
	postMerge := map[string]gomutant.Finding{}
	contradicted := map[string]bool{}
	priorLayer := map[string]string{}
	for _, f := range prior {
		priorLayer[f.Symbol], _ = docStore.Layer(f)
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		Budget: o.budget, OracleTimeout: o.oracleTimeout, OracleMemoryBytes: oracleMemoryBytes(o.oracleMemoryMiB), Jobs: o.jobs, Force: o.force, BracketPaths: o.bracketPaths, Prior: prior,
		PlanOnly: o.plan,
		Executing: func(event gomutant.ExecutionEvent) {
			renderExecutionEvent(out, event)
		},
		Decision: func(decision gomutant.RunDecision) {
			if !o.plan {
				renderRunDecision(out, decision)
				return
			}
			switch decision.Action {
			case "measure":
				planMeasure++
				planCandidates += decision.Candidates
			case "cached":
				planCached++
			case "skipped":
				planSkipped++
			}
			renderRunDecision(out, decision)
		},
		Progress: func(event gomutant.PreparationEvent) {
			renderPreparation(out, event)
		},
		Guidance: func(g gomutant.OracleGuidance) {
			fmt.Fprintf(out, "guidance  %s  unstable oracle evidence (%s): %s\n", g.Symbol, g.Reason, g.Suggestion)
		},
		Contradiction: func(c gomutant.AttestationContradiction) {
			contradicted[c.Symbol+"\x00"+c.Position+"\x00"+c.Operator] = true
			fmt.Fprintf(out, "contradiction  %s  attested survivor %s (%s) killed by %s; attestation shed (was: %s)\n", c.Symbol, c.Position, c.Operator, c.Killer, c.Reason)
		},
		AttestationSiteShed: func(d gomutant.AttestationShed) {
			commitSheds = append(commitSheds, d)
		},
		PropertyOracle: func(n gomutant.PropertyOracleNote) {
			fmt.Fprintf(out, "property  %s  %s: %s\n", n.Package, n.Runtime, n.Note)
		},
		// Each finished target commits under the same document lock the final
		// merge takes, so an interrupted run keeps its completed targets; the
		// final merge below remains the authority (REQ-exec-cancellation).
		// Plan mode suppresses this at the library boundary — the run owns
		// the plan clause's no-write guarantee.
		Commit: func(finding gomutant.Finding) error {
			return docStore.Update(ctx, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// The incremental commit is where a cross-site shed
				// actually happens against the prior document - the final
				// merge sees an already-stripped record, so the shed must
				// be collected here or it is silent (REQ-attest-survivor).
				merged, dropped := gomutant.MergeFindingsShedAgainst(current, []gomutant.Finding{finding}, attestSnapshot)
				commitSheds = append(commitSheds, dropped...)
				for _, m := range merged {
					if m.Symbol == finding.Symbol {
						postMerge[finding.Symbol] = m
					}
				}
				return merged, nil
			})
		},
	})
	var drift *gomutant.TreeDriftError
	if err != nil && !errors.As(err, &drift) {
		return err
	}
	// The final merge runs before anything renders: the output reads
	// the rows the document actually holds - a disposition recorded
	// concurrently between a symbol's incremental commit and the end of
	// the run is in both or in neither (REQ-mcp-findings-doc).
	var finalSheds []gomutant.AttestationShed
	if !o.plan {
		if err := docStore.Update(ctx, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var merged []gomutant.Finding
			if wholeTree {
				merged, finalSheds = gomutant.MergeWholeFindingsShedAgainst(current, findings, targets, attestSnapshot)
			} else {
				merged, finalSheds = gomutant.MergeFindingsShedAgainst(current, findings, attestSnapshot)
			}
			for _, m := range merged {
				if _, ran := postMerge[m.Symbol]; ran {
					postMerge[m.Symbol] = m
				}
			}
			return merged, nil
		}); err != nil {
			return err
		}
	}
	rendered := gomutant.RenderedFindings(findings, postMerge)
	for _, f := range rendered {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case f.Skipped != "":
			// The skip already printed as its decision line; a second
			// identical row earns nothing (REQ-exec-run-status's
			// dedup arm).
		case f.Cached:
			fmt.Fprintf(&terminal, "cached    %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()))
		default:
			fmt.Fprintf(&terminal, "measured  %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()))
		}
		if f.Skipped == "" {
			// Whether the record is safe to stage is answered here, not by
			// inspecting JSON (REQ-result-layers): a record the store routes
			// to the machine-local overlay names its disqualifier, so a run
			// that rendered healthy counts never leaves the repo document
			// silently missing the record.
			if layer, reason := docStore.Layer(f); layer == "local" {
				fmt.Fprintf(&terminal, "          machine-local: %s\n", reason)
			}
		}
		for _, s := range f.Open() {
			if s.Execution != "" {
				fmt.Fprintf(&terminal, "          survivor %s %s  [%s]\n", s.Position, s.Operator, s.Execution)
				continue
			}
			fmt.Fprintf(&terminal, "          survivor %s %s\n", s.Position, s.Operator)
		}
		for _, summary := range f.Operators {
			fmt.Fprintf(&terminal, "          operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
	}
	// A plan renders its own tallies; the zeroed run summary would
	// claim a measurement that never happened (REQ-exec-plan).
	if !o.plan {
		renderRunSummary(&terminal, gomutant.SummarizeRun(rendered))
	}
	// The class line earns its place only when it aggregates: a single
	// skip's decision line already said everything.
	if classes, skips := skipClasses(findings); skips > 1 {
		fmt.Fprintf(&terminal, "skipped   %s\n", classes)
	}
	if o.plan {
		fmt.Fprintf(&terminal, "plan      %d measure (%d candidates), %d cached, %d skipped\n", planMeasure, planCandidates, planCached, planSkipped)
		fmt.Fprintln(&terminal, "plan only: no baselines probed, no mutants executed, nothing persisted")
	} else {
		// A shed disposition is surfaced once, never silently dropped
		// (REQ-attest-survivor): the first report wins, and a mutant
		// whose fate the contradiction line already told - killed
		// evidence with the shed reasoning attached - is not retold
		// with the merge layer's vaguer reason.
		for _, d := range gomutant.DedupeAttestationSheds(append(append([]gomutant.AttestationShed(nil), commitSheds...), finalSheds...)) {
			if contradicted[d.Symbol+"\x00"+d.Position+"\x00"+d.Operator] {
				continue
			}
			fmt.Fprintf(&terminal, "attestation shed: %s %s %s - %s\n", d.Symbol, d.Position, d.Operator, d.Reason)
		}
		// A record this run carried from the machine-local overlay into
		// the committed document is a state change git does not see until
		// committed, so the run says it happened (REQ-mcp-findings-doc).
		promoted := 0
		for symbol, merged := range postMerge {
			if layer, _ := docStore.Layer(merged); layer == "repo" && priorLayer[symbol] == "local" {
				promoted++
			}
		}
		if promoted > 0 {
			fmt.Fprintf(&terminal, "%d record(s) promoted - findings document changed, commit it\n", promoted)
		}
	}
	if _, err := io.Copy(out, &terminal); err != nil {
		return err
	}
	// A drift-refused campaign keeps its rendered completed findings and
	// still fails operationally: a pipeline never reads a partial
	// campaign as success (REQ-exec-quiescence).
	if drift != nil {
		return drift
	}
	return nil
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

func renderExecutionEvent(w io.Writer, event gomutant.ExecutionEvent) {
	line := fmt.Sprintf("%-9s target %d/%d %s", event.Phase, event.TargetIndex, event.TargetCount, event.Symbol)
	// A confirming window's candidate tally is saturated by
	// construction - the confirmations counter is the signal - so the
	// line drops the dead segment (display only; the event carries the
	// tallies unchanged).
	if event.Phase != "confirming" {
		line += fmt.Sprintf("  candidates %d/%d", event.CandidatesDone, event.CandidatesTotal)
	}
	if event.ConfirmationsTotal > 0 {
		line += fmt.Sprintf("  confirmations %d/%d", event.ConfirmationsDone, event.ConfirmationsTotal)
	}
	fmt.Fprintln(w, line)
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

// skipClasses aggregates skip reasons so a targets-fed run reports
// each class once with its count instead of a row per symbol.
func skipClasses(findings []gomutant.Finding) (string, int) {
	counts := map[string]int{}
	var order []string
	for _, f := range findings {
		if f.Skipped == "" {
			continue
		}
		if counts[f.Skipped] == 0 {
			order = append(order, f.Skipped)
		}
		counts[f.Skipped]++
	}
	parts := make([]string, 0, len(order))
	total := 0
	for _, reason := range order {
		parts = append(parts, fmt.Sprintf("%d x %s", counts[reason], reason))
		total += counts[reason]
	}
	return strings.Join(parts, "; "), total
}

func renderRunSummary(w io.Writer, summary gomutant.RunSummary) {
	fmt.Fprintf(w, "summary   %d targets: %d measured, %d cached, %d skipped; %d generated, %d killed, %d survived, %d discarded; %d attested, %d open\n",
		summary.Targets, summary.Measured, summary.Cached, summary.Skipped, summary.Generated, summary.Killed, summary.Survived, summary.Discarded, summary.Attested, summary.Open)
}

// oracleMemoryBytes converts the MiB flag: 0 stays 0 (derive), negative
// stays negative (disabled).
func oracleMemoryBytes(mib int64) int64 {
	if mib <= 0 {
		return mib
	}
	return mib << 20
}
