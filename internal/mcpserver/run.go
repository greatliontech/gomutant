package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// capRunFindings builds the run response's finding rows under the
// envelope caps (REQ-mcp-envelope): a campaign multiplies every list;
// the document on disk carries the full set, so the response counts
// what it drops instead of inlining it. Candidate evidence is
// drill-down via the findings tool, not run payload. Each measured or
// cached row states its persistence layer (REQ-result-layers): whether
// the record is safe to stage is answered by the response, never by a
// second findings call after a run that rendered healthy counts while
// the store routed the record to the machine-local overlay.
func capRunFindings(ctx context.Context, findings []gomutant.Finding, layer func(gomutant.Finding) (string, string), onDelta func(gomutant.Finding) ([]gomutant.Survivor, error)) (rows []findingOut, omitted, deltaOpen int, err error) {
	for _, f := range findings {
		// The render bound is consulted per row, as the CLI's loop
		// consults it: the bound alone ends a render that outlives it
		// (the context is detached from the request), cut or no cut.
		if err := ctx.Err(); err != nil {
			return rows, len(findings) - len(rows), deltaOpen, err
		}
		// The cut is derived for every measured or cached record — the
		// summary's total counts rows past the cap too — and listed on
		// the rows within it.
		var delta []gomutant.Survivor
		if onDelta != nil && f.Skipped == "" {
			if delta, err = onDelta(f); err != nil {
				return nil, 0, 0, err
			}
			deltaOpen += len(delta)
		}
		if len(rows) == envelope.rows {
			omitted++
			continue
		}
		open := f.Open()
		omittedOpen := 0
		if len(open) > envelope.open {
			omittedOpen = len(open) - envelope.open
			open = open[:envelope.open]
		}
		row := findingOut{
			oracleExecutionPolicyOut: oracleExecutionPolicyRow(f),
			Symbol:                   f.Symbol, Selection: f.Selection, Labels: f.Labels,
			CandidateCount: f.CandidateCount, Generated: f.Generated,
			Mutants: f.Mutants, Killed: f.Killed, Discarded: f.Discarded,
			Attested: len(f.Attested), Withdrawn: len(f.Withdrawn), Open: open,
			OmittedOpen: omittedOpen,
			Cached:      f.Cached, Skipped: f.Skipped, Run: f.Run,
		}
		if f.Skipped == "" {
			row.Layer, row.LayerReason = layer(f)
			if onDelta != nil {
				if len(delta) > envelope.open {
					row.OmittedDeltaOpen = len(delta) - envelope.open
					delta = delta[:envelope.open]
				}
				row.DeltaOpen = delta
			}
		}
		rows = append(rows, row)
	}
	return rows, omitted, deltaOpen, nil
}

// guidanceOut is one oracle set's instability attribution shared by the
// targets it covers: the chunk-level memo already computes one
// attribution per set, so the response aggregates instead of repeating
// a near-identical suggestion per target (REQ-mcp-envelope).
type guidanceOut struct {
	Targets        []string `json:"targets" jsonschema:"targets whose unverifiable evidence this attribution covers; capped with the remainder counted"`
	OmittedTargets int      `json:"omittedTargets,omitempty" jsonschema:"covered targets beyond the per-row cap"`
	UnstableTests  []string `json:"unstableTests,omitempty" jsonschema:"capped with the remainder counted"`
	OmittedTests   int      `json:"omittedUnstableTests,omitempty" jsonschema:"unstable tests beyond the per-row cap"`
	Reason         string   `json:"reason,omitempty" jsonschema:"the first covered finding's unverifiable reason"`
	Attribution    string   `json:"attribution,omitempty" jsonschema:"the first covered finding's refusal attribution: the operation, its logged name, and the producing process's directory"`
	// UnstableAttributions names each unstable test's own solo run's
	// refusal attribution, by test.
	UnstableAttributions map[string]string `json:"unstableAttributions,omitempty" jsonschema:"each unstable test's own solo run's refusal attribution, by test"`
	Suggestion           string            `json:"suggestion"`
}

// contradictionOut is one shed attestation report (a re-measurement's added
// or moved tests killed an attested survivor).
type contradictionOut struct {
	Symbol   string `json:"symbol"`
	Position string `json:"position"`
	Operator string `json:"operator"`
	Killer   string `json:"killer,omitempty"`
	Reason   string `json:"reason"`
}

// appendGuidance folds a per-target attribution into its oracle set's
// aggregated entry, keyed by the suggestion and unstable set.
func appendGuidance(entries *[]guidanceOut, g gomutant.OracleGuidance) {
	key := g.Suggestion + "\x00" + strings.Join(g.UnstableTests, "\x00")
	for i := range *entries {
		existing := (*entries)[i]
		if existing.Suggestion+"\x00"+strings.Join(existing.UnstableTests, "\x00") == key {
			(*entries)[i].Targets = append(existing.Targets, g.Symbol)
			return
		}
	}
	*entries = append(*entries, guidanceOut{Targets: []string{g.Symbol}, UnstableTests: g.UnstableTests, Reason: g.Reason, Attribution: g.Attribution, UnstableAttributions: g.UnstableAttributions, Suggestion: g.Suggestion})
}

// runStreams routes the run's preparation and decision streams: with a
// progress token they ride notifications and leave the response, their
// totals remaining as counts; without one they stay inline, capped
// (REQ-mcp-envelope). lastPhase feeds the heartbeat.
type runStreams struct {
	out       *runOut
	notify    func(string)
	lastPhase *stretchLabel
	// analysisMu serializes the analysis hook, exempt from the run's
	// callback lock by its own contract.
	analysisMu *sync.Mutex
}

// analysisEventMessage renders an analysis event for the advisory
// notification channel; a non-empty detail (the per-subject
// analysis-unavailable provenance, the unlisted-toolchain notice)
// rides the same line after an em dash, matching the CLI face's
// rendering.
func analysisEventMessage(event gomutant.AnalysisEvent) string {
	return "analysis " + event.Text()
}

// stretch names the stretch the heartbeat reports as still working —
// the one label setter every phase of the call goes through, so a
// test can pin the sequence without racing the cadence.
func (r runStreams) stretch(label string) { r.lastPhase.set(label) }

func newRunStreams(out *runOut, notify func(string)) runStreams {
	return runStreams{out: out, notify: notify, lastPhase: newStretchLabel(gomutant.StretchPreparation), analysisMu: &sync.Mutex{}}
}

func (r runStreams) decision(decision gomutant.RunDecision) {
	r.out.DecisionsCount++
	// A decision opens no stretch: a served or skipped target is not
	// an executing one; the heartbeat keeps the last stretch named.
	if r.notify != nil {
		r.notify(decisionMessage(decision))
		return
	}
	r.out.Decisions, _ = appendCapped(r.out.Decisions, envelope.streamed, decision)
}

// analysis is the run tool's one analysis-event hook: a token hears
// every event; without one, a payload-bearing event — a distinct fact
// no face may discard at the source (REQ-exec-run-status) — rides the
// response inline at the row bound with the remainder counted, while
// the detail-free keep-alives, the throttled kind with no channel to
// keep alive here, are dropped (REQ-mcp-envelope). The hook is exempt
// from the run's callback serialization by the library's own contract,
// so it serializes itself.
func (r runStreams) analysis(event gomutant.AnalysisEvent) {
	if label, ok := event.Stretch(); ok {
		// A keep-alive names the heartbeat's stretch — the pass's unit
		// and position, in the CLI cadence line's words; a fact about
		// an operation or a diagnostic leaves the label where it was.
		r.stretch(label)
	}
	if r.notify != nil {
		if event.Detail != "" {
			r.analysisMu.Lock()
			r.out.AnalysisCount++
			r.analysisMu.Unlock()
		}
		r.notify(analysisEventMessage(event))
		return
	}
	if event.Detail == "" {
		return
	}
	r.analysisMu.Lock()
	defer r.analysisMu.Unlock()
	r.out.AnalysisCount++
	var kept bool
	if r.out.AnalysisEvents, kept = appendCapped(r.out.AnalysisEvents, envelope.rows, event); !kept {
		r.out.OmittedAnalysisEvents++
	}
}

// appendCapped appends v while the list is under the bound, reporting
// whether it was kept — the one inline-without-a-token policy.
func appendCapped[T any](list []T, bound int, v T) ([]T, bool) {
	if len(list) >= bound {
		return list, false
	}
	return append(list, v), true
}

// executing is the run tool's one execution-event hook: it names the
// heartbeat's stretch from the event's own Stretch, and while a token
// listens it forwards the advisory execution-phase events in the one
// grammar (REQ-exec-run-status's advisory classes) — the probe phase's
// priced announcement once (the per-batch probe ticks name the stretch
// only), every other class as its text, the confirmation mode on the
// line whenever the event carries one.
func (r runStreams) executing(e gomutant.ExecutionEvent) {
	if stretch := e.Stretch(); stretch != "" {
		r.stretch(stretch)
	}
	if r.notify == nil || (e.Phase == "probing" && e.ProbesDone != 0) {
		return
	}
	suffix := ""
	if e.ConfirmationMode != "" {
		// The gate state rides every face: the disarmed stride
		// must be distinguishable from the armed one for MCP
		// operators too (REQ-exec-run-status).
		suffix = " mode=" + e.ConfirmationMode
	}
	if label, rest, ok := e.Text("", suffix); ok {
		r.notify(label + " " + rest)
	}
}

func (r runStreams) progress(event gomutant.PreparationEvent) {
	r.out.PreparationCount++
	r.stretch(gomutant.StretchPreparing(event))
	if r.notify != nil {
		r.notify(preparationMessage(event))
		return
	}
	r.out.Preparation, _ = appendCapped(r.out.Preparation, envelope.streamed, event)
}

type runIn struct {
	selectionIn
	TargetsPath       string   `json:"targets_path,omitempty"`
	TargetsJSON       string   `json:"targets_json,omitempty"`
	Changed           string   `json:"changed,omitempty"`
	Budget            int      `json:"budget,omitempty"`
	TimeoutSec        *int     `json:"timeout_sec,omitempty"`
	AnalysisBudgetSec int      `json:"analysis_budget_sec,omitempty"`
	Jobs              int      `json:"jobs,omitempty"`
	BracketPaths      []string `json:"bracket_paths,omitempty"`
	ScratchNamespaces []string `json:"scratch_namespaces,omitempty"`
	OracleMemoryMiB   *int64   `json:"oracle_memory_mib,omitempty"`
	Staged            bool     `json:"staged,omitempty"`
	Force             bool     `json:"force,omitempty"`
	Findings          string   `json:"findings,omitempty"`
	Packages          []string `json:"packages,omitempty"`
	Symbols           []string `json:"symbols,omitempty"`
}

type findingOut struct {
	oracleExecutionPolicyOut
	Symbol           string                   `json:"symbol"`
	Selection        string                   `json:"selection,omitempty"`
	Labels           []string                 `json:"labels,omitempty"`
	CandidateCount   int                      `json:"candidateCount"`
	Generated        int                      `json:"generated"`
	Mutants          int                      `json:"mutants"`
	Killed           int                      `json:"killed"`
	Discarded        int                      `json:"discarded"`
	Attested         int                      `json:"attested,omitempty"`
	Withdrawn        int                      `json:"withdrawn,omitempty"`
	Open             []gomutant.Survivor      `json:"open,omitempty"`
	OmittedOpen      int                      `json:"omittedOpen,omitempty" jsonschema:"open survivors beyond the response cap; the findings tool serves the full set"`
	Cached           bool                     `json:"cached,omitempty"`
	Skipped          string                   `json:"skipped,omitempty"`
	DeltaOpen        []gomutant.Survivor      `json:"deltaOpen,omitempty" jsonschema:"on a changed-ref run, the open survivors on lines the working tree added since the ref - a subset of open, listed distinctly; capped like open"`
	OmittedDeltaOpen int                      `json:"omittedDeltaOpen,omitempty" jsonschema:"on-delta survivors beyond the response cap"`
	Run              string                   `json:"run,omitempty" jsonschema:"identity of the run that last measured any candidate of the record: this run's (the summary's run) on a measured row and on a cached row whose flagged candidates this run re-executed, the measuring run's on a wholly served row; absent on records measured before runs carried one"`
	Reuse            string                   `json:"reuse,omitempty" jsonschema:"the record's reuse posture — current, stale, unverifiable, or detached — judged over the run's own views: a committed record is not thereby reusable evidence"`
	Reasons          []gomutant.PostureReason `json:"reasons,omitempty" jsonschema:"each channel refusing reuse with its reason, composing: freshness (the judgment against the current tree), candidate evidence (stated beside a freshness refusal too), runtime inputs, and stored observation (the capture-time fact, stated beside the others when reuse is refused, it holds, and its text is not the judgment's own)"`
	Analysis         string                   `json:"analysis,omitempty" jsonschema:"what a later judgment needs: a re-measure, a re-execution of the affected evidence, nothing a re-judgment can lift, or a re-judgment once a tree that failed to load does"`
	Layer            string                   `json:"layer,omitempty" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay; absent on skipped targets"`
	LayerReason      string                   `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
}

type runOut struct {
	Summary                   gomutant.RunSummary         `json:"summary"`
	Exit                      string                      `json:"exit,omitempty" jsonschema:"set when the campaign ended on a cancellation after measurement began — the exit cause; summary.banked carries what the run committed, split between the findings document and the machine-local overlay; the findings list is empty, and the two layers hold every committed row"`
	Document                  string                      `json:"document"`
	Findings                  []findingOut                `json:"findings"`
	OmittedFindings           int                         `json:"omittedFindings,omitempty" jsonschema:"finding rows beyond the response cap; the document carries the full set"`
	Guidance                  []guidanceOut               `json:"oracleGuidance,omitempty" jsonschema:"oracle-instability attributions aggregated per oracle set: targets sharing one unstable oracle share one entry"`
	OmittedGuidance           int                         `json:"omittedOracleGuidance,omitempty" jsonschema:"guidance rows beyond the response cap - counted, never silent"`
	Contradictions            []contradictionOut          `json:"attestationContradictions,omitempty" jsonschema:"attested survivors a re-measurement's added or moved tests killed: each attestation was shed because evidence beats attestation, and the equivalence judgment wants re-review"`
	OmittedContradictions     int                         `json:"omittedAttestationContradictions,omitempty" jsonschema:"contradiction rows beyond the response cap; the findings tool serves every open survivor"`
	PropertyOracles           []string                    `json:"propertyOracles,omitempty" jsonschema:"property-runtime prerequisite statements per oracle package: what the run pinned itself (rapid: seed and reproducer files), or what the caller must ensure (gopter: an in-suite fixed seed) for reproducible verdicts"`
	OmittedPropertyOracles    int                         `json:"omittedPropertyOracles,omitempty" jsonschema:"property-oracle rows beyond the response cap"`
	AttestationSheds          []string                    `json:"attestationSheds,omitempty" jsonschema:"dispositions shed with the cause named - the mutation domain moved, the site content under the position changed, or the attested survivor is no longer reported - re-review and re-attest if genuinely equivalent"`
	OmittedAttestationSheds   int                         `json:"omittedAttestationSheds,omitempty" jsonschema:"shed rows beyond the response cap; every shed mutant is one of the document's open survivors"`
	AttestationCarries        []string                    `json:"attestationCarries,omitempty" jsonschema:"dispositions carried across moved measurement pins: the mutated source is unchanged and the mutant survived re-execution, so the equivalence reasoning rides - auditable acceptances, no action needed"`
	OmittedAttestationCarries int                         `json:"omittedAttestationCarries,omitempty" jsonschema:"carry rows beyond the response cap; the document's attestations carry the reasoning"`
	Promoted                  int                         `json:"promoted,omitempty" jsonschema:"records this run carried from the machine-local overlay into the committed findings document - the document changed, commit it"`
	Demoted                   int                         `json:"demoted,omitempty" jsonschema:"standing committed records this run's final write re-judged or re-measured into the machine-local overlay (an exemption withdrawn, a bracket no longer holding, a fresh measurement whose evidence is unverifiable) - a row leaving the findings document, a change git only sees when committed"`
	MachineLocalOnly          int                         `json:"machineLocalOnly,omitempty" jsonschema:"records this run routed to the machine-local overlay - the repo findings document gains nothing from them until their per-record disqualifiers clear; the capped findings list may omit some, this count never does"`
	Residue                   []gomutant.Residue          `json:"residue,omitempty"`
	OmittedResidue            int                         `json:"omittedResidue,omitempty"`
	Preparation               []gomutant.PreparationEvent `json:"preparation,omitempty" jsonschema:"absent when a progress token streamed the events; preparationCount still totals them"`
	PreparationCount          int                         `json:"preparationCount"`
	Decisions                 []gomutant.RunDecision      `json:"decisions,omitempty" jsonschema:"absent when a progress token streamed the decisions; decisionsCount still totals them"`
	DecisionsCount            int                         `json:"decisionsCount"`
	AnalysisEvents            []gomutant.AnalysisEvent    `json:"analysisEvents,omitempty" jsonschema:"payload-bearing freshness-analysis events - a failing baseline's own output, a per-subject analysis-unavailable provenance, an unlisted-toolchain notice - kept inline when no progress token streamed them; capped at the row bound, the remainder counted"`
	OmittedAnalysisEvents     int                         `json:"omittedAnalysisEvents,omitempty" jsonschema:"analysis events beyond the inline cap - counted, never silent"`
	AnalysisCount             int                         `json:"analysisCount" jsonschema:"payload-bearing analysis events the run emitted, streamed or inline; analysisEvents carries them inline up to the row bound"`
	Note                      string                      `json:"note,omitempty" jsonschema:"set when the run measured nothing (names the input that selected zero targets and the next step) or when a whole-tree reconcile dropped records whose targets left the code"`
	LegacyOverlays            []gomutant.LegacyEntry      `json:"legacyOverlays,omitempty" jsonschema:"machine-local overlay entries preserved unread because their document version predates this binary's range: an older gomutant's records, attested dispositions included, never served and never deleted; capped, the overlay directory holds the full set"`
	OmittedUnreached          int                         `json:"omittedUnreached,omitempty" jsonschema:"unreached symbols beyond the summary's row cap - counted, never silent; the findings document's coverage-bounds table holds the full roster"`
	OmittedLegacyOverlays     int                         `json:"omittedLegacyOverlays,omitempty" jsonschema:"legacy overlay rows beyond the response cap - counted, never silent"`
	ExemptionsRekeyed         []gomutant.RekeyedExemption `json:"exemptionsRekeyed,omitempty" jsonschema:"reviewed exemption entries whose clause this call's write re-keyed to canonical path or bracket-root spelling - the subject, the clause as it was, the clause as it matches now; present exactly when the record was rewritten"`
	OmittedExemptionsRekeyed  int                         `json:"omittedExemptionsRekeyed,omitempty" jsonschema:"re-keyed entries beyond the response cap - counted, not listed"`
}

// capAdvisories bounds the run response's five advisory lists and each
// guidance row's nested lists at the envelope caps with counted
// remainders (REQ-mcp-envelope), returning the FULL shed list so the
// drift error's own exemplar-bounded fold never shrinks with the
// response.
func (out *runOut) capAdvisories() (fullSheds []string) {
	fullSheds = out.AttestationSheds
	for i := range out.Guidance {
		if n := len(out.Guidance[i].Targets); n > envelope.nested {
			out.Guidance[i].OmittedTargets = n - envelope.nested
			out.Guidance[i].Targets = out.Guidance[i].Targets[:envelope.nested:envelope.nested]
		}
		if n := len(out.Guidance[i].UnstableTests); n > envelope.nested {
			out.Guidance[i].OmittedTests = n - envelope.nested
			out.Guidance[i].UnstableTests = out.Guidance[i].UnstableTests[:envelope.nested:envelope.nested]
			// The per-test attributions follow the kept tests: an
			// omitted test's rides out with it, so the map never
			// carries what the list's cap counted omitted.
			kept := make(map[string]string, len(out.Guidance[i].UnstableTests))
			for _, test := range out.Guidance[i].UnstableTests {
				if attribution, ok := out.Guidance[i].UnstableAttributions[test]; ok {
					kept[test] = attribution
				}
			}
			out.Guidance[i].UnstableAttributions = kept
		}
	}
	out.Guidance, out.OmittedGuidance = capRows(out.Guidance)
	out.Contradictions, out.OmittedContradictions = capRows(out.Contradictions)
	out.PropertyOracles, out.OmittedPropertyOracles = capRows(out.PropertyOracles)
	out.AttestationSheds, out.OmittedAttestationSheds = capRows(out.AttestationSheds)
	out.AttestationCarries, out.OmittedAttestationCarries = capRows(out.AttestationCarries)
	return fullSheds
}

// recordOutcome states the document changes the final merge made — the
// promoted and demoted counts and the machine-local aggregate — on the
// response, one composition for every path that writes: the measuring
// run and the zero-target reconcile alike (REQ-mcp-findings-doc,
// REQ-result-local-signpost).
func (out *runOut) recordOutcome(outcome gomutant.RunOutcome) {
	out.Promoted = outcome.Promoted
	out.Demoted = outcome.Demoted
	out.MachineLocalOnly = outcome.MachineLocal
	out.ExemptionsRekeyed, out.OmittedExemptionsRekeyed = rekeyedRows(outcome.ExemptionsRekeyed)
}

func (s *Server) toolRun(ctx context.Context, req *mcp.CallToolRequest, in runIn) (result *mcp.CallToolResult, out runOut, err error) {
	// One heartbeat spans the whole call — the load, the selection and
	// its signposts, the run, the merge, the rendering — naming the
	// stretch the streams last recorded, so no phase goes silent longer
	// than the cadence while a token listens (REQ-mcp-envelope).
	notify := progressNotifier(ctx, req)
	streams := newRunStreams(&out, notify)
	_, err = withHeartbeatLabel(ctx, notify, streams.lastPhase.get, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.runTool(ctx, in, streams)
	})
	return nil, out, err
}

// runTool is the run call's body: the streams carry the notifier and
// the response it writes into — one response, whichever path fills it.
func (s *Server) runTool(ctx context.Context, in runIn, streams runStreams) (err error) {
	notify, out := streams.notify, streams.out
	callStart := time.Now()
	// The run's identity is minted first: every record this run measures
	// carries it, and the summary names it even when nothing was
	// selected (REQ-exec-run-status).
	runID := gomutant.NewRunID()
	out.Summary.Run = runID
	timeout, err := commandTimeout("timeout_sec", in.TimeoutSec)
	if err != nil {
		return err
	}
	analysisBudget, err := secondsDuration("analysis_budget_sec", in.AnalysisBudgetSec)
	if err != nil {
		return err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	defer func() {
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil && !errors.Is(err, contextErr) {
				// Wrap, never replace: the underlying cause (a partial
				// commit note, a version-ahead refusal) survives beside
				// the deadline attribution.
				err = fmt.Errorf("%w: %v", contextErr, err)
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Every refusal the inputs decide fires here, before the load
	// (REQ-exec-preparation); the target-source exclusivity counts the
	// inline document as a targets source.
	sources := wireTargetSources(in.TargetsPath, in.TargetsJSON, in.Changed)
	inputs := s.targetInputs(in.TargetsPath, in.TargetsJSON, in.Changed)
	prepared, err := gomutant.PrepareCampaign(ctx, gomutant.CampaignInputs{
		FindingsPath: gomutant.FindingsPathAt(s.dir, in.Findings), ModuleDir: s.dir, Selection: in.selection(),
		Budget: in.Budget, AnalysisBudget: analysisBudget,
		ScratchNamespaces: in.ScratchNamespaces, BracketPaths: in.BracketPaths,
		TargetSources: sources, Targets: inputs, Packages: in.Packages, Symbols: in.Symbols, CutChanged: true,
	})
	if err != nil {
		return err
	}
	defer prepared.ReleaseCampaign()
	scratchNamespaces, exemptions := prepared.ScratchNamespaces, prepared.Exemptions
	// The loading event rides the inline face when no token listens;
	// with one, the load itself announces it (loadTreeReporting).
	out.PreparationCount++
	if notify == nil {
		out.Preparation = append(out.Preparation, loadingEvent)
	}
	streams.stretch(gomutant.StretchPreparing(loadingEvent))
	if notify != nil {
		notify(preparationMessage(loadingEvent))
	}
	tree, err := s.loadTreeContext(ctx, in.selection())
	if err != nil {
		return err
	}
	var targets []gomutant.Target
	wholeTree := false
	streams.stretch(gomutant.StretchSelecting)
	sel, err := s.selectTargets(ctx, tree, prepared.Request)
	if err != nil {
		return err
	}
	targets, wholeTree = sel.Targets, sel.WholeTree
	out.Residue = sel.Residue
	prior := prepared.Prior
	// The ledger is the run's document side on both faces
	// (REQ-attest-survivor, REQ-mcp-findings-doc); its writes take the
	// server's document seam.
	ledger := gomutant.NewRunLedger(prepared.Store, prior, runID, wholeTree)
	ledger.PruneSymbol = sel.PruneSymbol
	ledger.Update = func(ctx context.Context, change func([]gomutant.Finding) ([]gomutant.Finding, error)) (gomutant.Routing, error) {
		return s.updateStore(ctx, prepared.Store, gomutant.FindingsPathAt(s.dir, in.Findings), change)
	}
	out.LegacyOverlays, out.OmittedLegacyOverlays = capRows(prepared.Store.LegacyEntries())
	if out.Residue, err = tree.OracleClosureSignpostContext(ctx, out.Residue, prior, targets, func(stage string) {
		stretch := gomutant.StretchInspecting(stage)
		streams.stretch(stretch)
		if notify != nil {
			notify(stretch)
		}
	}); err != nil {
		return err
	}
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		out.Residue, out.OmittedResidue = capRows(out.Residue)
		out.Document = gomutant.FindingsPathAt(s.dir, in.Findings)
		out.Note = gomutant.SelectionEmptiedNote(in.TargetsPath != "" || in.TargetsJSON != "", in.Changed, "changed")
		// A whole-tree selection of nothing still reconciles the
		// document — a write the response owns, never buried in an
		// empty success; a scoped one writes nothing.
		if wholeTree {
			streams.stretch(gomutant.StretchReconciling)
		}
		outcome, err := ledger.Finish(ctx, nil, nil, tree.Selection())
		if err != nil {
			return err
		}
		// The zero-target write re-judges the standing records too: a
		// promotion or a demotion on it is the response's to state
		// (REQ-mcp-findings-doc).
		out.recordOutcome(outcome)
		if line := outcome.DropText(); line != "" {
			out.Note += "; " + line
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// A shed is recorded the moment its strip persists, never under the
	// findings-document lock — the notification writes to the client
	// transport, and a slow client must not extend the hold. Delivery on
	// abort: the SDK discards the typed output when the handler errors,
	// so the abort returns below ride the recorded sheds on the error
	// itself (the drift-refusal precedent); a client that sent a
	// progress token additionally hears each one as it lands
	// (REQ-attest-survivor's "loudly, in every mode").
	ledger.Shed = func(d gomutant.AttestationShed) {
		line := d.Text()
		out.AttestationSheds = append(out.AttestationSheds, line)
		if notify != nil {
			notify("attestation shed: " + line)
		}
	}
	if seams.afterCommit != nil {
		ledger.Committed = seams.afterCommit
	}
	postures := map[string]gomutant.RecordPosture{}
	var tallies *gomutant.RunTallies
	options := gomutant.Options{
		RunID:             runID,
		Budget:            in.Budget,
		AnalysisBudget:    analysisBudget,
		Jobs:              in.Jobs,
		Force:             in.Force,
		BracketPaths:      in.BracketPaths,
		ScratchNamespaces: scratchNamespaces,
		Exemptions:        exemptions,
		Staged:            in.Staged,
		OwnWrites:         gomutant.RunOwnWrites(gomutant.FindingsPathAt(s.dir, in.Findings)),
		OracleMemoryBytes: mcpOracleMemoryBytes(in.OracleMemoryMiB),
		Guidance:          func(g gomutant.OracleGuidance) { appendGuidance(&out.Guidance, g) },
		Contradiction: func(c gomutant.AttestationContradiction) {
			ledger.Contradiction(c)
			out.Contradictions = append(out.Contradictions, contradictionOut{
				Symbol: c.Symbol, Position: c.Position, Operator: c.Operator, Killer: c.Killer, Reason: c.Reason,
			})
			if notify != nil {
				notify("contradiction " + c.Symbol + " " + c.Text())
			}
		},
		AttestationSiteShed: ledger.SiteShed,
		AttestationCarried: func(c gomutant.AttestationCarry) {
			line := c.Text()
			out.AttestationCarries = append(out.AttestationCarries, line)
			if notify != nil {
				notify("attestation carried: " + line)
			}
		},
		PropertyOracle: func(n gomutant.PropertyOracleNote) {
			out.PropertyOracles = append(out.PropertyOracles, fmt.Sprintf("%s %s: %s", n.Package, n.Runtime, n.Note))
		},
		Prior:     prior,
		Decision:  streams.decision,
		Posture:   func(p gomutant.RecordPosture) { postures[p.Symbol] = p },
		Tallies:   func(r gomutant.RunTallies) { tallies = &r },
		Progress:  streams.progress,
		Executing: streams.executing,
		// Each finished target commits under the same document lock the final
		// merge takes, so an interrupted run keeps its completed targets; the
		// final merge below remains the authority (REQ-exec-cancellation).
		Commit: ledger.Commit(ctx),
		Layer:  ledger.Layer,
	}
	options.AnalysisEvent = streams.analysis
	// The heartbeat keeps long compile and execution stretches audible
	// under the client's deadline: no phase goes silent longer than the
	// cadence while a token listens (REQ-mcp-envelope).
	findings, err := tree.Run(ctx, targets, options)
	var drift *gomutant.TreeDriftError
	if err == nil {
		err = ctx.Err()
	}
	if err != nil && !errors.As(err, &drift) {
		// A cancellation after measurement began is the campaign's
		// ordinary end on this face: the result carries the banked
		// state — what the document kept, under the exit cause — and
		// the sheds the incremental commits recorded, so nothing the
		// SDK would discard with an error is lost
		// (REQ-exec-banked-summary; REQ-mcp-envelope). A run cancelled
		// before measurement began has nothing banked and errors.
		if tallies == nil {
			return ridingAbort(err, out)
		}
		out.Summary = bankedRunSummary(*tallies, gomutant.ExitCause(err), time.Since(callStart), tree.Selection(), runID)
		out.Exit = out.Summary.Banked.Cause
		// The sheds the incremental commits recorded are already on the
		// result (the ledger delivered each as its strip persisted); the
		// same epilogue as a completed run caps
		// every advisory list and the residue (REQ-mcp-envelope).
		out.capAdvisories()
		out.Residue, out.OmittedResidue = capRows(out.Residue)
		out.Document = gomutant.FindingsPathAt(s.dir, in.Findings)
		return nil
	}
	// The final merge runs before anything renders: the response reads
	// the rows the document actually holds - a disposition recorded
	// concurrently between a symbol's incremental commit and the end of
	// the run is in both or in neither (REQ-mcp-findings-doc).
	streams.stretch(gomutant.StretchMerging)
	outcome, err := ledger.Finish(ctx, findings, targets, tree.Selection())
	// A failed final merge persisted nothing (Finish returns the zero
	// outcome on error): the exit carries the sheds and payloads already
	// on the response, whose object the SDK discards (REQ-mcp-envelope).
	if err != nil {
		return ridingAbort(err, out)
	}
	// From here every error exit owns what the final merge PERSISTED —
	// the reconcile's drop, a promotion, the merge's residue sheds —
	// folded into the error text exactly as the response's sheds and
	// the recorded analysis payloads are (REQ-mcp-findings-doc,
	// REQ-mcp-envelope). A shed disposition is surfaced once, never
	// silently dropped (REQ-attest-survivor): the first report wins - a
	// shed the incremental commit already recorded, or a mutant whose
	// fate the contradiction row already told, is not retold with the
	// merge layer's vaguer reason.
	for _, d := range outcome.ResidueSheds {
		out.AttestationSheds = append(out.AttestationSheds, d.Text())
	}
	if seams.afterFinalReplacement != nil {
		seams.afterFinalReplacement()
	}
	// The final replacement is the success boundary
	// (REQ-exec-cancellation): rendering after it runs under a context
	// detached from the request's deadline and bounded by its own, so
	// the request's deadline never fails a committed run; the bound's
	// own expiry ends the render carrying what the write persisted.
	ctx, cancelRender := gomutant.PostCommitRenderContext(ctx, seams.postCommitRenderBound)
	defer cancelRender()
	streams.stretch(gomutant.StretchRendering)
	rendered := outcome.Rendered
	out.Summary = gomutant.SummarizeRun(rendered, tree.Selection(), tree.PackageOf)
	out.Summary.Run = runID
	out.Summary.AddPostures(postures)
	if tallies != nil && tallies.Audit.Narrowed > 0 {
		audit := tallies.Audit
		out.Summary.Audit = &audit
	}
	// The summary's unreached roster caps like every row list, the
	// remainder counted (REQ-mcp-envelope).
	out.Summary.Unreached, out.OmittedUnreached = capRows(out.Summary.Unreached)
	// The cut is derived once per row; the summary sums the rows. The
	// rows beyond the response cap are cut too, so the total is exact.
	var onDelta func(gomutant.Finding) ([]gomutant.Survivor, error)
	if sel.Cut != nil {
		onDelta = func(f gomutant.Finding) ([]gomutant.Survivor, error) {
			split, err := tree.CutSurvivorsContext(ctx, f, *sel.Cut)
			return split.OnDelta, err
		}
	}
	var deltaOpen int
	out.Findings, out.OmittedFindings, deltaOpen, err = capRunFindings(ctx, rendered, ledger.Layer, onDelta)
	for i := range out.Findings {
		if p, ok := postures[out.Findings[i].Symbol]; ok {
			out.Findings[i].Reuse, out.Findings[i].Reasons, out.Findings[i].Analysis = string(p.Reuse), p.Reasons, p.Analysis
		}
	}
	if sel.Cut != nil && err == nil {
		out.Summary.Delta = &gomutant.DeltaSummary{Ref: sel.Cut.Ref, Open: deltaOpen}
	}
	out.Residue, out.OmittedResidue = capRows(out.Residue)
	// A record this run moved between the layers — into the committed
	// document or out of it — is a state change git does not see until
	// committed, so the response says it happened (REQ-mcp-findings-doc).
	out.recordOutcome(outcome)
	out.Document = gomutant.FindingsPathAt(s.dir, in.Findings)
	if line := outcome.DropText(); line != "" {
		out.Note = line
	}
	if err != nil {
		return outcome.PersistedRiding(ridingAbort(err, out))
	}
	fullSheds := out.capAdvisories()
	// A drift-refused campaign persists its completed findings and still
	// errors: the client never reads a partial campaign as success
	// (REQ-exec-quiescence). The SDK renders only the error text on
	// failure, so consequences already stripped from the document -
	// attestation sheds - fold into it via driftError: surfaced once,
	// never silently dropped (REQ-attest-survivor).
	if drift != nil {
		return outcome.PersistedRiding(analysisRidingAbort(driftError(drift, fullSheds), out.AnalysisEvents, out.AnalysisCount))
	}
	return nil
}

// cappedSheds bounds an error-riding shed list with the remainder
// counted (REQ-mcp-envelope): a field campaign can shed dozens of
// dispositions, and an error string is a signal surface, not the
// document - the findings document names every shed mutant as open.
func cappedSheds(sheds []string) string {
	const exemplars = 5
	if len(sheds) <= exemplars {
		return strings.Join(sheds, "; ")
	}
	return fmt.Sprintf("%s; ... (+%d more - the shed mutants are the document's open survivors)", strings.Join(sheds[:exemplars], "; "), len(sheds)-exemplars)
}

// shedsRidingAbort attaches persisted attestation sheds to an abort's
// error: the SDK discards the typed output when a handler errors, so the
// error text is the one channel an aborted run's client is guaranteed to
// read - the incremental commits kept the stripped records, and the
// strips must reach the caller loudly (REQ-attest-survivor).
func shedsRidingAbort(err error, sheds []string) error {
	if len(sheds) == 0 {
		return err
	}
	return fmt.Errorf("%w; attestation sheds persisted before this abort (re-review and re-attest if genuinely equivalent): %s", err, cappedSheds(sheds))
}

// ridingAbort composes what an aborted run must not lose onto its
// error, the SDK discarding the typed output when the handler errors:
// the attestation sheds persisted before the abort, and the analysis
// payloads a tokenless run recorded (REQ-attest-survivor,
// REQ-exec-run-status) — one composition at every abort return.
func ridingAbort(err error, out *runOut) error {
	return analysisRidingAbort(shedsRidingAbort(err, out.AttestationSheds), out.AnalysisEvents, out.AnalysisCount)
}

// analysisRidingAbort folds the inline analysis payloads into an abort:
// a tokenless run has no other channel for them — a failing baseline's
// own output must reach the reader (REQ-exec-run-status). Exemplars,
// bounded as the sheds are: each payload's first line, the remainder
// counted from the run's total, which the inline cap never cuts.
func analysisRidingAbort(err error, events []gomutant.AnalysisEvent, total int) error {
	if len(events) == 0 {
		return err
	}
	const exemplars = 5
	var heads []string
	for i, e := range events {
		if i == exemplars {
			break
		}
		head, _, _ := strings.Cut(strings.TrimSpace(e.Detail), "\n")
		subject := e.Phase
		if e.Package != "" {
			subject += " " + e.Package
		}
		heads = append(heads, subject+": "+head)
	}
	rest := ""
	if total > exemplars {
		rest = fmt.Sprintf(" (+%d more)", total-exemplars)
	}
	return fmt.Errorf("%w; analysis payloads seen before this abort: %s%s", err, strings.Join(heads, "; "), rest)
}

// driftError folds attestation sheds into a drift refusal: the SDK
// renders only the error text on failure, and the document already
// stripped the sheds, so this text is their one surfacing - never
// silently dropped (REQ-attest-survivor).
func driftError(drift error, sheds []string) error {
	if len(sheds) == 0 {
		return drift
	}
	return fmt.Errorf("%w; attestation sheds riding this refusal (re-review and re-attest if genuinely equivalent): %s", drift, cappedSheds(sheds))
}
