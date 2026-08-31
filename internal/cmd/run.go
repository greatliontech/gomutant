package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/spf13/cobra"
)

// syncWriter serializes Write calls from the run's concurrent render
// sources onto one underlying writer.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

type runOptions struct {
	dir, changed, targetsFile, findingsFile  string
	packages, symbols                        []string
	tags                                     []string
	toolchain                                string
	budget, jobs                             int
	oracleMemoryMiB                          int64
	timeout, oracleTimeout                   time.Duration
	force, plan, staged, jsonl               bool
	progressEvery                            time.Duration
	bracketPaths, scratchNamespaces, vouches []string
	output                                   io.Writer
}

func newRunCommand() *cobra.Command {
	o := runOptions{}
	cmd := &cobra.Command{Use: "run", Short: guidanceShort("run"), Long: guidanceHelp("run"), Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runCommand(cmd.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", ".", "tree root (module or workspace)")
	f.IntVar(&o.budget, "budget", 0, "candidates per symbol; 0 = exhaustive")
	f.DurationVar(&o.timeout, "timeout", 0, "cancel command work before result commit after this duration; 0 = unlimited")
	f.DurationVar(&o.oracleTimeout, "oracle-timeout", 0, "maximum duration of each oracle process; 0 derives each oracle group's budget from its measured baseline (an explicit value is the uniform override)")
	f.Int64Var(&o.oracleMemoryMiB, "oracle-memory-mib", 0, "memory ceiling per oracle process tree in MiB (GOMEMLIMIT plus a hard data-segment cap): 0 derives RAM/(2 x jobs) floored at 1 GiB, -1 disables; a runaway-allocation mutant dies on its own ceiling as an ordinary kill instead of OOMing the host")
	f.IntVar(&o.jobs, "jobs", 0, "concurrent mutant runs; 0 = half the CPUs")
	f.StringArrayVar(&o.bracketPaths, "bracket-path", nil, "external surface the oracle legitimately reads (module-relative path or absolute file, repeatable; absolute directories and tool-excluded paths are refused); extends each spawn's observation bracket, carrying the caller's assertion the surface is mutation-free for the run")
	f.StringArrayVar(&o.scratchNamespaces, "scratch-namespace", nil, "in-module run-scratch namespace DIR:PATTERN (repeatable): DIR is module-relative, PATTERN a single-component os.MkdirTemp-style name pattern; oracle scratch minted and removed inside the namespace stops recording per-run missing-arm noise, forfeiting exactly the appearance-pin of absence-probes the pattern matches - the caller's assertion; malformed declarations refuse before any measurement")
	f.StringArrayVar(&o.vouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, recorded on the evidence")
	f.BoolVar(&o.staged, "staged", false, "measure the git index snapshot: staged-but-uncommitted content counts clean and the finding records the index tree identity; unstaged drift over a measured target's inputs refuses that target (stage or stash it)")
	f.BoolVar(&o.force, "force", false, "re-measure even targets whose prior finding still covers the request; the pin spans the mutated symbol's body, every oracle test's source closure, and the observed runtime inputs (toolchain, build configuration, and the other measurement pins are always compared too), so new or changed oracle tests re-measure without --force")
	f.StringVar(&o.changed, "changed", "", "target only symbols whose bodies differ from this git ref")
	f.StringVar(&o.targetsFile, "targets", "", "path to a JSON targets document (gomutant's or a producer's export); overrides discovery")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "findings document to read and update")
	f.StringArrayVar(&o.packages, "package", nil, "package import-path glob; repeatable")
	f.StringArrayVar(&o.symbols, "symbol", nil, "fully qualified symbol glob; repeatable")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.BoolVar(&o.jsonl, "jsonl", false, "structured output: every progress event, decision, result row, and summary as one JSON object per line — the CLI's machine-readable face; the human rendering is suppressed")
	f.DurationVar(&o.progressEvery, "progress-interval", 30*time.Second, "cadence of the cumulative progress line (targets committed, candidates, kills, elapsed); 0 disables")
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
	// The analysis heartbeat is invoked outside the library's callback
	// serialization (Options.AnalysisEvent is documented safe for
	// concurrent invocation), so it can write concurrently with the
	// callback-rendered decision and progress lines; one serialized
	// writer keeps every line whole and race-free.
	out = &syncWriter{w: out}
	if err := ctx.Err(); err != nil {
		return err
	}
	rep := newRunReporter(out, o.jsonl, 0)
	defer rep.stop()
	if o.jsonl {
		rep.emit("prepare", gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	} else {
		renderPreparation(out, gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	}
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	scratchNamespaces, err := gomutant.ParseScratchNamespaces(o.scratchNamespaces)
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
	rep.setSelected(len(targets))
	if !o.plan {
		rep.startCadence(o.progressEvery)
	}
	docPath := findingsAt(o.dir, o.findingsFile)
	// The campaign lock spans measurement through the final merge:
	// a second campaign against the same document refuses immediately
	// instead of interleaving (REQ-exec-exclusivity).
	releaseCampaign, err := gomutant.AcquireCampaignLock(docPath)
	if err != nil {
		return err
	}
	defer releaseCampaign()
	exemptions, err := gomutant.LoadExemptions(gomutant.ExemptionsPathFor(docPath))
	if err != nil {
		return err
	}
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
		if o.jsonl {
			// Residue rows flush FIRST — the same order the human
			// face prints them in.
			if err := rep.flushProse(terminal.String()); err != nil {
				return err
			}
			terminal.Reset()
			rep.emit("note", map[string]string{"text": "no targets"})
			if !o.plan {
				rep.emit("summary", gomutant.RunSummary{})
			}
		} else {
			fmt.Fprintln(&terminal, "no targets")
			if !o.plan {
				renderRunSummary(&terminal, gomutant.RunSummary{})
			}
		}
		if o.plan {
			// The plan clause's no-write guarantee covers the empty
			// whole-tree reconciliation too: a plan never prunes.
			fmt.Fprintln(&terminal, "plan only: no baselines probed, no mutants executed, nothing persisted")
			return rep.flushProse(terminal.String())
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
		return rep.flushProse(terminal.String())
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
	// A shed is reported the moment its strip persists: the incremental
	// commit survives an aborted run (that is its purpose), so a shed
	// buffered for the epilogue is silently dropped exactly when the
	// document kept the stripped record (REQ-attest-survivor's "loudly, in
	// every mode"). The epilogue renders only what no commit streamed - the
	// final merge's residue. A mutant whose fate the contradiction line
	// already told is not retold: execution precedes its target's commit,
	// so the filter is complete at print time.
	printedSheds := map[string]bool{}
	streamShed := func(d gomutant.AttestationShed) {
		key := d.Symbol + "\x00" + d.Position + "\x00" + d.Operator
		if printedSheds[key] || contradicted[key] {
			return
		}
		printedSheds[key] = true
		rep.line("attestation-shed", d, func(w io.Writer) {
			fmt.Fprintf(w, "attestation shed: %s %s %s - %s\n", d.Symbol, d.Position, d.Operator, d.Reason)
		})
	}
	var analysisMu sync.Mutex
	var analysisLast time.Time
	priorLayer := map[string]string{}
	for _, f := range prior {
		priorLayer[f.Symbol], _ = docStore.Layer(f)
	}
	// The first SIGINT drains: no new mutants, in-flight ones finish,
	// measured prefixes commit as candidate-capped records; the second
	// cancels hard (REQ-exec-cancellation's graceful-interrupt clause).
	var softStop <-chan struct{}
	soft := softInterruptFrom(ctx)
	if soft != nil {
		drain := make(chan struct{})
		softStop = drain
		soft.arm(func() {
			fmt.Fprintln(os.Stderr, "gomutant: interrupt - draining in-flight mutants and committing measured prefixes; interrupt again to cancel hard")
			close(drain)
		})
		// Disarmed the moment Run returns (below), so a SIGINT landing
		// during the final merge or rendering cancels hard instead of
		// closing a channel nothing reads; the defer is the panic net.
		defer soft.disarm()
	}
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		SoftStop: softStop,
		Budget:   o.budget, OracleTimeout: o.oracleTimeout, OracleMemoryBytes: oracleMemoryBytes(o.oracleMemoryMiB), Jobs: o.jobs, Force: o.force, BracketPaths: o.bracketPaths, ScratchNamespaces: scratchNamespaces, Exemptions: exemptions, Staged: o.staged, Prior: prior,
		OwnWrites: gomutant.RunOwnWrites(docPath),
		PlanOnly:  o.plan,
		Executing: func(event gomutant.ExecutionEvent) {
			rep.executing(event)
			if o.jsonl {
				rep.emit("execution", event)
				return
			}
			renderExecutionEvent(out, event, rep.selectionNote(event.TargetCount), rep.confirmationModeSuffix(event))
		},
		Decision: func(decision gomutant.RunDecision) {
			rep.decision(decision)
			if !o.plan {
				if o.jsonl {
					rep.emit("decision", decision)
					return
				}
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
			if o.jsonl {
				rep.emit("decision", decision)
				return
			}
			renderRunDecision(out, decision)
		},
		Progress: func(event gomutant.PreparationEvent) {
			if o.jsonl {
				rep.emit("prepare", event)
				return
			}
			renderPreparation(out, event)
		},
		// Detail-free events are the analysis keep-alive, time-gated
		// to a heartbeat: the run's longest silent stretches are
		// in-process gofresh analysis (the freshness and
		// producer-validation passes - the field reports'
		// "post-completion tail" burned hours there with no line
		// printed), and a ten-second heartbeat names the phase without
		// flooding a healthy run. Detail-bearing events are never
		// throttled: each is a distinct fact (the per-subject
		// analysis-unavailable provenance the field diagnoses from),
		// and the structured face keeps the package a package with
		// the payload under its own key.
		AnalysisEvent: func(phase, pkg, detail string) {
			analysisMu.Lock()
			defer analysisMu.Unlock()
			if detail == "" {
				now := time.Now()
				if now.Sub(analysisLast) < 10*time.Second {
					return
				}
				analysisLast = now
				if o.jsonl {
					rep.emit("analysis", map[string]string{"phase": phase, "package": pkg})
					return
				}
				fmt.Fprintf(out, "analysis  %s\n", strings.TrimSpace(analysisPhrase(phase)+" "+pkg))
				return
			}
			if o.jsonl {
				rep.emit("analysis", map[string]string{"phase": phase, "package": pkg, "detail": detail})
				return
			}
			fmt.Fprintf(out, "analysis  %s\n", strings.TrimSpace(analysisPhrase(phase)+" "+pkg+" — "+detail))
		},
		Guidance: func(g gomutant.OracleGuidance) {
			rep.line("guidance", g, func(w io.Writer) {
				fmt.Fprintf(w, "guidance  %s  unstable oracle evidence (%s): %s\n", g.Symbol, g.Reason, g.Suggestion)
			})
		},
		Contradiction: func(c gomutant.AttestationContradiction) {
			contradicted[c.Symbol+"\x00"+c.Position+"\x00"+c.Operator] = true
			rep.line("contradiction", c, func(w io.Writer) {
				fmt.Fprintf(w, "contradiction  %s  attested survivor %s (%s) killed by %s; attestation shed (was: %s)\n", c.Symbol, c.Position, c.Operator, c.Killer, c.Reason)
			})
		},
		AttestationSiteShed: func(d gomutant.AttestationShed) {
			commitSheds = append(commitSheds, d)
			streamShed(d)
		},
		AttestationCarried: func(c gomutant.AttestationCarry) {
			rep.line("attestation-carried", c, func(w io.Writer) {
				fmt.Fprintf(w, "attestation carried: %s %s %s - measurement pins moved; the mutated source is unchanged and the mutant survived re-execution\n", c.Symbol, c.Position, c.Operator)
			})
		},
		PropertyOracle: func(n gomutant.PropertyOracleNote) {
			rep.line("property", n, func(w io.Writer) {
				fmt.Fprintf(w, "property  %s  %s: %s\n", n.Package, n.Runtime, n.Note)
			})
		},
		// Each finished target commits under the same document lock the final
		// merge takes, so an interrupted run keeps its completed targets; the
		// final merge below remains the authority (REQ-exec-cancellation).
		// Plan mode suppresses this at the library boundary — the run owns
		// the plan clause's no-write guarantee.
		Commit: func(finding gomutant.Finding) error {
			var dropped []gomutant.AttestationShed
			err := docStore.Update(ctx, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// The incremental commit is where a cross-site shed
				// actually happens against the prior document - the final
				// merge sees an already-stripped record, so the shed must
				// be collected here or it is silent (REQ-attest-survivor).
				merged, shed := gomutant.MergeFindingsShedAgainst(current, []gomutant.Finding{finding}, attestSnapshot)
				dropped = shed
				for _, m := range merged {
					if m.Symbol == finding.Symbol {
						postMerge[finding.Symbol] = m
					}
				}
				return merged, nil
			})
			if err != nil {
				return err
			}
			// Banked ONLY after the update returned: a failed or
			// cancelled commit is work the findings document does not
			// hold, and the exit summary must never claim it
			// (REQ-exec-cancellation's claims-only-committed clause).
			rep.bankedFinding(finding)
			// Streamed after the update returns: the strip persisted, and
			// terminal writes must not extend the document-lock hold.
			commitSheds = append(commitSheds, dropped...)
			for _, d := range dropped {
				streamShed(d)
			}
			return nil
		},
	})
	if soft != nil {
		soft.disarm()
	}
	rep.stop()
	var drift *gomutant.TreeDriftError
	if err != nil && !errors.As(err, &drift) {
		// The banked-state exit summary (REQ-exec-cancellation's
		// rendering half): a budget, signal, or abort exit names what
		// the findings document kept instead of ending on a bare
		// context error. Only incrementally committed findings are
		// claimed.
		rep.bankedState(exitCause(err))
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
	localOnly := 0
	for _, f := range rendered {
		if err := ctx.Err(); err != nil {
			return err
		}
		var layer, layerReason string
		if f.Skipped == "" {
			// Whether the record is safe to stage is answered here, not by
			// inspecting JSON (REQ-result-layers): a record the store routes
			// to the machine-local overlay names its disqualifier, so a run
			// that rendered healthy counts never leaves the repo document
			// silently missing the record.
			if l, reason := docStore.Layer(f); l == "local" {
				localOnly++
				layer, layerReason = l, reason
			}
		}
		if o.jsonl {
			if f.Skipped != "" {
				continue // the decision event already carried the skip
			}
			rep.emit("result", resultRow(f, layer, layerReason))
			continue
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
		if layer == "local" {
			fmt.Fprintf(&terminal, "          machine-local: %s\n", layerReason)
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
		if o.jsonl {
			rep.emit("summary", gomutant.SummarizeRun(rendered))
		} else {
			renderRunSummary(&terminal, gomutant.SummarizeRun(rendered))
		}
	}
	// The class line earns its place only when it aggregates: a single
	// skip's decision line already said everything.
	if classes, skips := skipClasses(findings); skips > 1 {
		fmt.Fprintf(&terminal, "skipped   %s\n", classes)
	}
	// Whole-package blast radius, one line per dark package: the count
	// line above cannot distinguish scattered skips from a package
	// whose entire target set carries no campaign evidence.
	for _, radius := range gomutant.SkippedPackageRadius(rendered) {
		if radius.Dark() && radius.Targets > 1 {
			fmt.Fprintf(&terminal, "dark      %s: all %d targets skipped\n", radius.Package, radius.Targets)
		}
	}
	if o.plan {
		if o.jsonl {
			// The structured face carries the radius in plan mode too:
			// the run summary is suppressed there (a zeroed summary
			// would claim a measurement that never happened), so the
			// dark packages ride the plan payload
			// (REQ-result-skip-radius).
			rep.emit("plan", struct {
				Measure      int      `json:"measure"`
				Candidates   int      `json:"candidates"`
				Cached       int      `json:"cached"`
				Skipped      int      `json:"skipped"`
				DarkPackages []string `json:"darkPackages,omitempty"`
			}{planMeasure, planCandidates, planCached, planSkipped, gomutant.SummarizeRun(rendered).DarkPackages})
		}
		fmt.Fprintf(&terminal, "plan      %d measure (%d candidates), %d cached, %d skipped\n", planMeasure, planCandidates, planCached, planSkipped)
		fmt.Fprintln(&terminal, "plan only: no baselines probed, no mutants executed, nothing persisted")
	} else {
		// A shed disposition is surfaced once, never silently dropped
		// (REQ-attest-survivor): the first report wins - a shed the
		// incremental commit already streamed, or a mutant whose fate the
		// contradiction line already told (killed evidence with the shed
		// reasoning attached), is not retold with a vaguer reason. What
		// remains here is the final merge's residue.
		for _, d := range gomutant.DedupeAttestationSheds(append(append([]gomutant.AttestationShed(nil), commitSheds...), finalSheds...)) {
			key := d.Symbol + "\x00" + d.Position + "\x00" + d.Operator
			if contradicted[key] || printedSheds[key] {
				continue
			}
			if o.jsonl {
				// One wire shape per class: the final-merge residue
				// emits the same structured event the streamed sheds
				// do, never a prose note.
				rep.emit("attestation-shed", d)
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
		// The aggregate form of the per-record signpost, printed when
		// any record stayed machine-local: without it a run leaving
		// the repo document unchanged reads as a silent write failure
		// from outside (the field shape: real measured counts, an
		// empty committed document, no stated cause).
		if localOnly > 0 {
			fmt.Fprintf(&terminal, "%d record(s) machine-local only (disqualifiers named above) - the repo findings document gains nothing from them until the disqualifiers clear; a pre-commit loop can measure the staged index clean with --staged\n", localOnly)
		}
	}
	if err := rep.flushProse(terminal.String()); err != nil {
		return err
	}
	// A drift-refused campaign keeps its rendered completed findings and
	// still fails operationally: a pipeline never reads a partial
	// campaign as success (REQ-exec-quiescence).
	if drift != nil {
		return drift
	}
	// A broken structured-face pipe fails the command rather than
	// truncating the stream silently.
	return rep.firstWriteError()
}

func renderPreparation(w io.Writer, event gomutant.PreparationEvent) {
	switch event.Stage {
	case gomutant.PreparationLoading:
		fmt.Fprintln(w, "prepare   loading")
	case gomutant.PreparationBaseline:
		fmt.Fprintf(w, "prepare   %s %s %s\n", event.Stage, event.Symbol, event.Package)
	case gomutant.PreparationOracleBudget:
		fmt.Fprintf(w, "prepare   %s %s %s %s\n", event.Stage, event.Symbol, event.Package, event.OracleBudget)
	default:
		fmt.Fprintf(w, "prepare   %s %s\n", event.Stage, event.Symbol)
	}
}

func renderExecutionEvent(w io.Writer, event gomutant.ExecutionEvent, selectionNote, modeSuffix string) {
	if event.Phase == "confirmation-flip" {
		// A demoted kill is never silent: the serial re-run re-scored
		// this mutant a survivor and withdrew its provisional killer.
		fmt.Fprintf(w, "confirmation FLIP: %s %s - provisional kill by %s re-scored survivor on serial re-run\n",
			event.Symbol, event.FlipPosition, event.FlipKiller)
		return
	}
	line := fmt.Sprintf("%-9s target %d/%d %s", event.Phase, event.TargetIndex, event.TargetCount, event.Symbol)
	// The event's TargetCount is measure targets prepared so far, not
	// the request: the reporter's selection note beside it keeps a
	// resumed run's shrunken denominator honest ("7/71" reads as
	// remaining work of the same 85-target request, not a different
	// campaign).
	line += selectionNote
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
	line += modeSuffix
	fmt.Fprintln(w, line)
}

// exitCause names the exit path for the banked-state summary.
func exitCause(err error) string {
	switch {
	case errors.Is(err, gomutant.ErrInterrupted):
		// The graceful drain: measured prefixes are committed and
		// re-running the same command extends them — the one exit that
		// is neither a timeout, a hard cancel, nor an abort.
		return "interrupted gracefully - measured prefixes committed; re-run to extend"
	case errors.Is(err, context.DeadlineExceeded):
		return "command timeout"
	case errors.Is(err, context.Canceled):
		// The CLI's err descends from the ctx it passed, so a
		// Canceled error always accompanies a canceled ctx — one arm.
		return "interrupt/cancellation"
	default:
		return fmt.Sprintf("aborted (%v)", err)
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
