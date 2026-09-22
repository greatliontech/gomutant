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
	// runID overrides the minted run identity; tests pin output with it.
	runID                                    string
	dir, changed, targetsFile, findingsFile  string
	packages, symbols                        []string
	tags                                     []string
	toolchain                                string
	budget, jobs                             int
	oracleMemoryMiB                          int64
	timeout, oracleTimeout, analysisBudget   time.Duration
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
	f.StringVar(&o.dir, "dir", ".", "")
	f.IntVar(&o.budget, "budget", 0, "")
	f.DurationVar(&o.timeout, "timeout", 0, "")
	f.DurationVar(&o.oracleTimeout, "oracle-timeout", 0, "")
	f.DurationVar(&o.analysisBudget, "analysis-budget", 0, "")
	f.Int64Var(&o.oracleMemoryMiB, "oracle-memory-mib", 0, "")
	f.IntVar(&o.jobs, "jobs", 0, "")
	f.StringArrayVar(&o.bracketPaths, "bracket-path", nil, "")
	f.StringArrayVar(&o.scratchNamespaces, "scratch-namespace", nil, "")
	f.StringArrayVar(&o.vouches, "vouch", nil, "")
	f.BoolVar(&o.staged, "staged", false, "")
	f.BoolVar(&o.force, "force", false, "")
	f.StringVar(&o.changed, "changed", "", "")
	f.StringVar(&o.targetsFile, "targets", "", "")
	f.StringVar(&o.findingsFile, "findings", defaultFindings, "")
	f.StringArrayVar(&o.packages, "package", nil, "")
	f.StringArrayVar(&o.symbols, "symbol", nil, "")
	selectionFlags(f, &o.tags, &o.toolchain)
	f.BoolVar(&o.jsonl, "json", false, "")
	progressIntervalFlag(f, &o.progressEvery)
	f.BoolVar(&o.plan, "plan", false, "")
	return knobbedFlags(cmd, "run")
}

func runCommand(ctx context.Context, o runOptions) error {
	if o.timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if o.oracleTimeout < 0 {
		return fmt.Errorf("oracle timeout must not be negative")
	}
	if o.analysisBudget < 0 {
		return fmt.Errorf("analysis budget must not be negative")
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
	// The run's identity leads: every record this run measures carries
	// it, and an inspection scopes to the campaign by it
	// (REQ-exec-run-status).
	runID := o.runID
	if runID == "" {
		runID = gomutant.NewRunID()
	}
	if err := rep.flushProse(renderRunIdentity(runID)); err != nil {
		return err
	}
	if trimmed := strings.TrimSpace(o.targetsFile); strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return fmt.Errorf("--targets expects a file path; the value looks like an inline JSON document - write it to a file first")
	}
	// The cadence starts before the preparation: a slow refusal, the
	// lock, or a long typed load is a stretch the progress line must
	// name, not a silence.
	rep.phase(gomutant.StretchPreparation)
	rep.startCadence(o.progressEvery)
	// Every refusal the inputs decide fires here, before the load: the
	// bounds, the target sources and the document they name, the
	// declarations, the tree root and the changed ref's surface, the
	// harness environment, the exemptions, the store, and last the
	// campaign lock (REQ-exec-preparation).
	docPath := gomutant.FindingsPathAt(o.dir, o.findingsFile)
	sources := gomutant.TargetSourcesGiven(gomutant.TargetSource{Name: "--targets", Given: o.targetsFile != ""}, gomutant.TargetSource{Name: "--changed", Given: o.changed != ""})
	// The target inputs are read by the preparation at their enumerated
	// places — the document's parse, then the ref's surface once the
	// root exists — before the lock and the load; a plan renders no cut.
	prepared, err := gomutant.PrepareCampaign(ctx, gomutant.CampaignInputs{
		FindingsPath: docPath, ModuleDir: o.dir, Plan: o.plan, Selection: selectionOf(o.tags, o.toolchain),
		Budget: o.budget, OracleTimeout: o.oracleTimeout, AnalysisBudget: o.analysisBudget,
		ScratchNamespaces: o.scratchNamespaces, Vouches: o.vouches, BracketPaths: o.bracketPaths,
		TargetSources: sources, Targets: targetInputs(o.dir, o.targetsFile, o.changed),
		Packages: o.packages, Symbols: o.symbols, CutChanged: !o.plan,
	})
	if err != nil {
		return err
	}
	defer prepared.ReleaseCampaign()
	scratchNamespaces, exemptions, docStore, prior := prepared.ScratchNamespaces, prepared.Exemptions, prepared.Store, prepared.Prior
	if line := gomutant.LegacyOverlayLine(docStore.LegacyEntries()); line != "" {
		if err := rep.flushProse(line + "\n"); err != nil {
			return err
		}
	}
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	tree, err := gomutant.LoadContextSelection(ctx, o.dir, selectionOf(o.tags, o.toolchain))
	if err != nil {
		return err
	}
	if len(prepared.Vouches) > 0 {
		tree.SetDynamicStateVouches(prepared.Vouches...)
	}
	// A stretch is named before the work it names begins, on both faces.
	rep.phase(gomutant.StretchSelecting)
	selected, err := tree.SelectTargets(ctx, prepared.Request)
	if err != nil {
		return err
	}
	targets, residue, cut, wholeTree := selected.Targets, selected.Residue, selected.Cut, selected.WholeTree
	rep.setSelected(len(targets))
	if residue, err = tree.OracleClosureSignpostContext(ctx, residue, prior, targets, func(stage string) { rep.phase(gomutant.StretchInspecting(stage)) }); err != nil {
		return err
	}
	var terminal bytes.Buffer
	for _, r := range residue {
		fmt.Fprintf(&terminal, "changed, untargeted  %s  (%s)\n", r.Path, r.Reason)
	}
	// The ledger is the run's document side on both faces
	// (REQ-attest-survivor, REQ-mcp-findings-doc).
	ledger := gomutant.NewRunLedger(docStore, prior, runID, wholeTree)
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
			rep.emit("note", map[string]string{"text": "no targets: " + gomutant.SelectionEmptiedNote(o.targetsFile != "", o.changed, "--changed")})
			if !o.plan {
				rep.emit("summary", gomutant.RunSummary{Run: runID})
			}
		} else {
			fmt.Fprintln(&terminal, "no targets: "+gomutant.SelectionEmptiedNote(o.targetsFile != "", o.changed, "--changed"))
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
		// A whole-tree selection of nothing still reconciles the
		// document — the write that drops records whose targets left
		// the code, stated here as on the other face; a scoped one
		// writes nothing.
		if wholeTree {
			rep.phase(gomutant.StretchReconciling)
		}
		outcome, err := ledger.Finish(ctx, nil, nil, tree.Selection())
		if err != nil {
			return err
		}
		renderPromoted(&terminal, outcome)
		renderReconcileDrop(&terminal, outcome)
		return rep.flushProse(terminal.String())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var planMeasure, planCandidates, planCached, planSkipped int
	// A shed reaches the terminal the moment its strip persists, never
	// under the document lock; the epilogue renders only the final
	// merge's residue (REQ-attest-survivor's "loudly, in every mode").
	ledger.Shed = func(d gomutant.AttestationShed) {
		rep.line("attestation-shed", d, func(w io.Writer) {
			fmt.Fprintf(w, "attestation shed: %s\n", d.Text())
		})
	}
	// Banked only after the update returned: the exit summary claims
	// committed work alone (REQ-exec-cancellation).
	ledger.Committed = rep.bankedFinding
	var analysisMu sync.Mutex
	var analysisLast time.Time
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
	postures := map[string]gomutant.RecordPosture{}
	var tallies *gomutant.RunTallies
	findings, err := tree.Run(ctx, targets, gomutant.Options{
		RunID:    runID,
		SoftStop: softStop,
		Budget:   o.budget, OracleTimeout: o.oracleTimeout, AnalysisBudget: o.analysisBudget, OracleMemoryBytes: gomutant.OracleMemoryBytesFromMiB(o.oracleMemoryMiB), Jobs: o.jobs, Force: o.force, BracketPaths: o.bracketPaths, ScratchNamespaces: scratchNamespaces, Exemptions: exemptions, Staged: o.staged, Prior: prior,
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
		Posture: func(p gomutant.RecordPosture) {
			postures[p.Symbol] = p
			if o.jsonl && !o.plan {
				rep.emit("posture", p)
			}
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
		Progress: rep.preparation,
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
		AnalysisEvent: func(event gomutant.AnalysisEvent) {
			// Every keep-alive names the cadence line's stretch, the
			// throttled ones included: the line reports the latest
			// unit, the log only every tenth second's.
			rep.analysis(event)
			analysisMu.Lock()
			defer analysisMu.Unlock()
			if event.Detail == "" {
				now := time.Now()
				if now.Sub(analysisLast) < 10*time.Second {
					return
				}
				analysisLast = now
				if o.jsonl {
					rep.emit("analysis", event)
					return
				}
				fmt.Fprintf(out, "analysis  %s\n", event.Head())
				return
			}
			if o.jsonl {
				rep.emit("analysis", event)
				return
			}
			renderAnalysis(out, event)
		},
		Guidance: func(g gomutant.OracleGuidance) {
			rep.line("guidance", g, func(w io.Writer) {
				fmt.Fprintf(w, "guidance  %s  unstable oracle evidence (%s): %s\n", g.Symbol, g.Reason, g.Suggestion)
			})
		},
		Contradiction: func(c gomutant.AttestationContradiction) {
			ledger.Contradiction(c)
			rep.line("contradiction", c, func(w io.Writer) {
				fmt.Fprintf(w, "contradiction  %s  %s\n", c.Symbol, c.Text())
			})
		},
		AttestationSiteShed: ledger.SiteShed,
		AttestationCarried: func(c gomutant.AttestationCarry) {
			rep.line("attestation-carried", c, func(w io.Writer) {
				fmt.Fprintf(w, "attestation carried: %s\n", c.Text())
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
		// The run's own tallies: the banked state a cancelled run
		// reports and the audit rate the summary carries, one count for
		// both faces (REQ-exec-banked-summary).
		Tallies: func(r gomutant.RunTallies) { tallies = &r },
		// Plan mode suppresses this at the library boundary — the run owns
		// the plan clause's no-write guarantee.
		Commit: ledger.Commit(ctx),
	})
	if soft != nil {
		soft.disarm()
	}
	rep.stop()
	var drift *gomutant.TreeDriftError
	if err != nil && !errors.As(err, &drift) {
		// The banked-state exit summary (REQ-exec-banked-summary): a
		// budget, signal, or abort exit names what the findings
		// document kept instead of ending on a bare context error —
		// the run's own tallies, claiming only returned commits; a run
		// cancelled before measurement began has no tallies and stays
		// silent.
		if tallies != nil {
			rep.bankedState(tallies.Banked(gomutant.ExitCause(err), time.Since(rep.start)))
		}
		return err
	}
	// The final merge runs before anything renders: the output reads
	// the rows the document actually holds - a disposition recorded
	// concurrently between a symbol's incremental commit and the end of
	// the run is in both or in neither (REQ-mcp-findings-doc).
	var outcome gomutant.RunOutcome
	if o.plan {
		outcome.Rendered = ledger.Rendered(findings)
	} else {
		var err error
		if outcome, err = ledger.Finish(ctx, findings, targets, tree.Selection()); err != nil {
			return err
		}
		if seams.afterFinalReplacement != nil {
			seams.afterFinalReplacement()
		}
		// The final replacement is the success boundary
		// (REQ-exec-cancellation): rendering after it runs detached
		// from the command's deadline and interrupt under its own
		// bound, so the command's deadline or interrupt never fails a
		// committed run; the bound's own expiry ends the render carrying
		// what the write persisted. A plan replaces nothing and renders
		// under the command's own context.
		var cancelRender context.CancelFunc
		ctx, cancelRender = gomutant.PostCommitRenderContext(ctx, seams.postCommitRenderBound)
		defer cancelRender()
	}
	rendered := outcome.Rendered
	deltaOpen := 0
	// An error exit after the final merge persisted flushes what was
	// rendered so far with the merge's residue sheds (surfaced once,
	// never silently dropped — REQ-attest-survivor) and carries the
	// document changes it persisted in its text, as the structured face
	// does (REQ-mcp-findings-doc).
	exitAfterWrite := func(err error) error {
		renderResidueSheds(o, rep, &terminal, outcome)
		_ = rep.flushProse(terminal.String())
		return outcome.PersistedRiding(err)
	}
	for _, f := range rendered {
		if err := ctx.Err(); err != nil {
			return exitAfterWrite(err)
		}
		var layer, layerReason string
		if f.Skipped == "" {
			// Whether the record is safe to stage is answered here, not by
			// inspecting JSON (REQ-result-layers): a record the store routes
			// to the machine-local overlay names its disqualifier, so a run
			// that rendered healthy counts never leaves the repo document
			// silently missing the record.
			if l, reason := docStore.Layer(f); l == gomutant.LayerLocal {
				layer, layerReason = l, reason
			}
		}
		// A changed-ref run cuts the row's open survivors by the
		// delta's added lines, once per row; the summary sums the rows
		// (REQ-exec-run-status).
		var split gomutant.DeltaSurvivors
		var onDelta []gomutant.Survivor
		if cut != nil && f.Skipped == "" {
			var err error
			if split, err = tree.CutSurvivorsContext(ctx, f, *cut); err != nil {
				return exitAfterWrite(err)
			}
			onDelta = split.OnDelta
			deltaOpen += len(onDelta)
		}
		if o.jsonl {
			if f.Skipped != "" {
				continue // the decision event already carried the skip
			}
			rep.emit("result", resultRow(f, layer, layerReason, onDelta))
			continue
		}
		switch {
		case f.Skipped != "":
			// The skip already printed as its decision line; a second
			// identical row earns nothing (REQ-exec-run-status's
			// dedup arm).
		case f.Cached:
			// A served row names the run that last measured any of its
			// candidates: the measuring run's on a wholly served record,
			// this run's (the head line's) when the serve re-executed
			// flagged or drifted candidates (REQ-exec-run-status).
			fmt.Fprintf(&terminal, "cached    %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open%s%s\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()), deltaCount(cut, onDelta), runSuffix(f.Run))
		default:
			fmt.Fprintf(&terminal, "measured  %s  %d/%d candidates, %d mutants, %d killed, %d discarded, %d open%s\n", f.Symbol, f.Generated, f.CandidateCount, f.Mutants, f.Killed, f.Discarded, len(f.Open()), deltaCount(cut, onDelta))
		}
		if layer == gomutant.LayerLocal {
			fmt.Fprintf(&terminal, "          machine-local: %s\n", layerReason)
		}
		if p, ok := postures[f.Symbol]; ok && f.Skipped == "" && p.Reuse != gomutant.FindingCurrent {
			// Reuse is stated beside the counts: a committed record is
			// not thereby reusable evidence (REQ-result-run-posture).
			fmt.Fprintf(&terminal, "          reuse: %s\n", p.Line())
		}
		for i, s := range f.Open() {
			mark := ""
			if split.IsOnDelta(i) {
				mark = "  [delta]"
			}
			if s.Execution != "" {
				fmt.Fprintf(&terminal, "          survivor %s %s  [%s]%s\n", s.Position, s.Operator, s.Execution, mark)
				continue
			}
			fmt.Fprintf(&terminal, "          survivor %s %s%s\n", s.Position, s.Operator, mark)
		}
		for _, summary := range f.Operators {
			fmt.Fprintf(&terminal, "          operator %s: %d generated, %d killed, %d survived, %d discarded\n",
				summary.Operator, summary.Generated, summary.Killed, summary.Survived, summary.Discarded)
		}
	}
	// A plan renders its own tallies; the zeroed run summary would
	// claim a measurement that never happened (REQ-exec-plan).
	if !o.plan {
		summary := gomutant.SummarizeRun(rendered, tree.Selection())
		summary.Run = runID
		summary.AddPostures(postures)
		if cut != nil {
			summary.Delta = &gomutant.DeltaSummary{Ref: cut.Ref, Open: deltaOpen}
		}
		// The narrowed-survivor audit's measured rate rides the run
		// summary from the run's own tallies (REQ-exec-oracle-run's
		// narrowed-survivor clause).
		if tallies != nil && tallies.Audit.Narrowed > 0 {
			audit := tallies.Audit
			summary.Audit = &audit
		}
		if o.jsonl {
			rep.emit("summary", summary)
		} else {
			renderRunSummary(&terminal, summary)
			renderAudit(&terminal, summary)
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
		planSummary := gomutant.SummarizeRun(rendered, tree.Selection())
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
				Selection    string   `json:"selection,omitempty"`
				Unreached    []string `json:"unreached,omitempty"`
			}{planMeasure, planCandidates, planCached, planSkipped, planSummary.DarkPackages, planSummary.Selection, planSummary.Unreached})
		}
		fmt.Fprintf(&terminal, "plan      %d measure (%d candidates), %d cached, %d skipped\n", planMeasure, planCandidates, planCached, planSkipped)
		renderCoverageBound(&terminal, planSummary.Selection, planSummary.Unreached)
		fmt.Fprintln(&terminal, "plan only: no baselines probed, no mutants executed, nothing persisted")
	} else {
		renderResidueSheds(o, rep, &terminal, outcome)
		// A record this run carried from the machine-local overlay into
		// the committed document is a state change git does not see until
		// committed, so the run says it happened (REQ-mcp-findings-doc).
		renderPromoted(&terminal, outcome)
		renderReconcileDrop(&terminal, outcome)
		// The aggregate form of the per-record signpost, printed when
		// any record stayed machine-local: without it a run leaving
		// the repo document unchanged reads as a silent write failure
		// from outside (the field shape: real measured counts, an
		// empty committed document, no stated cause).
		if outcome.MachineLocal > 0 {
			fmt.Fprintf(&terminal, "%d record(s) machine-local only (disqualifiers named above) - the repo findings document gains nothing from them until the disqualifiers clear; a pre-commit loop can measure the staged index clean with --staged\n", outcome.MachineLocal)
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
	fmt.Fprintf(w, "prepare   %s\n", event.Text())
}

func renderExecutionEvent(w io.Writer, event gomutant.ExecutionEvent, selectionNote, modeSuffix string) {
	// The one grammar, under this face's column: the label padded to
	// the prefix width (REQ-exec-run-status).
	label, rest, ok := event.Text(selectionNote, modeSuffix)
	if !ok {
		return
	}
	fmt.Fprintf(w, "%-9s %s\n", label, rest)
}

func renderRunDecision(w io.Writer, decision gomutant.RunDecision) {
	label, rest := decision.Text()
	fmt.Fprintf(w, "%-9s %s\n", label, rest)
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

// renderAudit renders the summary's narrowed-survivor audit rate, the
// run's own count (REQ-exec-oracle-run's narrowed-survivor clause);
// a run that audited nothing renders no line.
func renderAudit(w io.Writer, summary gomutant.RunSummary) {
	if summary.Audit == nil {
		return
	}
	fmt.Fprintf(w, "audit     %d narrowed survivor(s) re-scored under the full oracle this run, %d disagreed\n", summary.Audit.Narrowed, summary.Audit.Disagreed)
}

func renderRunSummary(w io.Writer, summary gomutant.RunSummary) {
	fmt.Fprintf(w, "summary   %d targets: %d measured, %d cached, %d skipped; %d generated, %d killed, %d survived, %d discarded; %d attested, %d open",
		summary.Targets, summary.Measured, summary.Cached, summary.Skipped, summary.Generated, summary.Killed, summary.Survived, summary.Discarded, summary.Attested, summary.Open)
	if summary.Delta != nil {
		fmt.Fprintf(w, "; %d open on the delta of %s", summary.Delta.Open, summary.Delta.Ref)
	}
	fmt.Fprintln(w)
	renderCoverageBound(w, summary.Selection, summary.Unreached)
	renderReusePosture(w, summary)
}

// renderReusePosture states the summary's reuse posture: how many
// completed records are reusable as they stand and, capped, which are
// not and why — counts never stand for reuse (REQ-result-run-posture).
func renderReusePosture(w io.Writer, summary gomutant.RunSummary) {
	if summary.Reusable == 0 && len(summary.NotReusable) == 0 {
		return
	}
	fmt.Fprintf(w, "reuse     %d reusable as they stand, %d not", summary.Reusable, len(summary.NotReusable)+summary.OmittedNotReusable)
	if summary.OmittedNotReusable > 0 {
		fmt.Fprintf(w, " (%d listed)", len(summary.NotReusable))
	}
	fmt.Fprintln(w)
	for _, p := range summary.NotReusable {
		fmt.Fprintf(w, "          %s  %s\n", p.Symbol, p.Line())
	}
}

// plural is the count-aware noun suffix.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// unreachedShown bounds the symbols a bound line spells before counting
// the remainder: a roster a reader acts on, so wider than a summary
// line's three exemplars, and bounded so a whole dark leg never floods
// the terminal.
const unreachedShown = 20

// renderCoverageBound prints the declared selection's stated coverage
// bound under the summary: the unreached targets by symbol, bounded with
// the remainder counted, so the population the measurement covered is
// never read as whole (REQ-result-unreached-bound).
func renderCoverageBound(w io.Writer, selection string, unreached []string) {
	if len(unreached) == 0 {
		return
	}
	shown := unreached
	if len(shown) > unreachedShown {
		shown = shown[:unreachedShown]
	}
	line := fmt.Sprintf("unreached under selection %s: %d target%s no oracle of the selection's leg reaches — %s", selection, len(unreached), plural(len(unreached)), strings.Join(shown, ", "))
	if len(unreached) > unreachedShown {
		line += fmt.Sprintf(" (+%d more)", len(unreached)-unreachedShown)
	}
	fmt.Fprintln(w, line)
}

// deltaCount renders a row's on-delta open count beside its open
// count on a changed-ref run; empty on every other run.
func deltaCount(cut *gomutant.DeltaCut, onDelta []gomutant.Survivor) string {
	if cut == nil {
		return ""
	}
	return fmt.Sprintf(" (%d on the delta)", len(onDelta))
}

// renderRunIdentity is the run's first human line, the identity every
// record it measures carries (REQ-exec-run-status).
func renderRunIdentity(runID string) string {
	return "run       " + runID + "\n"
}

// runSuffix renders a record's run identity as a row suffix; empty for
// a record measured before runs carried one.
func runSuffix(run string) string {
	if run == "" {
		return ""
	}
	return "  [run " + run + "]"
}

// renderAnalysis prints a payload-bearing analysis event: one line with
// the detail when it fits one, else the line then the detail's lines
// indented under it (a baseline's output is many).
func renderAnalysis(w io.Writer, event gomutant.AnalysisEvent) {
	if !strings.Contains(strings.TrimRight(event.Detail, "\n"), "\n") {
		fmt.Fprintf(w, "analysis  %s\n", event.Text())
		return
	}
	fmt.Fprintf(w, "analysis  %s:\n", event.Head())
	for _, line := range strings.Split(strings.TrimRight(event.Detail, "\n"), "\n") {
		fmt.Fprintf(w, "          %s\n", line)
	}
}

// renderResidueSheds renders the final merge's residue sheds — a shed
// disposition is surfaced once, never silently dropped
// (REQ-attest-survivor): the first report wins, a shed the incremental
// commit already streamed, or a mutant whose fate the contradiction
// line already told (killed evidence with the shed reasoning attached),
// is not retold with a vaguer reason; what remains is the merge's
// residue. One wire shape per class: on the structured stream the
// residue emits the same event the streamed sheds do, never a prose
// note.
func renderResidueSheds(o runOptions, rep *runReporter, terminal io.Writer, outcome gomutant.RunOutcome) {
	for _, d := range outcome.ResidueSheds {
		if o.jsonl {
			rep.emit("attestation-shed", d)
			continue
		}
		fmt.Fprintf(terminal, "attestation shed: %s\n", d.Text())
	}
}

// renderPromoted states the records the run's writes carried from the
// machine-local overlay into the committed document — a state change
// git does not see until committed, so the run says it happened on
// every path that writes (REQ-mcp-findings-doc).
func renderPromoted(w io.Writer, outcome gomutant.RunOutcome) {
	if line := outcome.PromotedText(); line != "" {
		fmt.Fprintln(w, line)
	}
}

// renderReconcileDrop states a whole-tree reconcile's persisted drop:
// records whose targets left the code are a document change git does
// not show until committed, so the run owns it on this face as the
// structured face does (REQ-mcp-envelope).
func renderReconcileDrop(w io.Writer, outcome gomutant.RunOutcome) {
	if line := outcome.DropText(); line != "" {
		fmt.Fprintln(w, line)
	}
}

// targetInputs are the CLI's target sources as given: the document's
// path as typed, a changed ref through the git seam.
func targetInputs(dir, targetsFile, changed string) gomutant.TargetInputs {
	in := gomutant.TargetInputs{TargetsPath: targetsFile}
	if changed != "" {
		in.Changed = gitref.ChangedSelection(dir, changed)
	}
	return in
}
