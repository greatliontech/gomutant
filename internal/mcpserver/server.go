// Package mcpserver serves gomutant over the Model Context Protocol: the
// library's operations as tools, a thin shell exactly like the CLI so the two
// faces cannot drift (spec mcp.md). It inherits the advisory stance whole —
// no tool renders a pass/fail verdict (REQ-result-findings).
package mcpserver

import (
	guidancepkg "github.com/greatliontech/gofresh/guidance"

	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/gitref"
)

// clientKeepAliveInterval paces the server's keepalive pings - the
// disconnect detector for in-flight campaigns. A variable so tests can
// tighten it; production always uses the default.
var clientKeepAliveInterval = 30 * time.Second

// Server is a dir-bound MCP server over the gomutant library.
type Server struct {
	dir            string
	updateDocument func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) error

	// mu guards the loaded-tree cache. The cached Tree is read-only after
	// load and served to concurrent tool calls; see loadTreeContext for the
	// reuse constraint.
	mu      sync.Mutex
	tree    *gomutant.Tree
	treeKey string
	vouches []string
	// widthMu guards the in-flight campaigns' shared claim on the
	// process-wide oracle-parallelism width and the ephemeral probe's
	// process-wide overrides: the first campaign's job count owns the
	// width until every claiming run exits, and a probe's override
	// excludes campaigns entirely for its duration.
	widthMu        sync.Mutex
	widthJobs      int
	widthClaims    int
	probeOverrides int
}

// claimRunWidth admits a run into the shared process-width claim: the
// first in-flight campaign's job count owns the width; a concurrent
// run requesting a different count refuses loudly instead of splitting
// the owner's oracle environments from its evidence environment
// (REQ-exec-oracle-parallelism).
func (s *Server) claimRunWidth(jobs int) error {
	resolved := resolveJobs(jobs)
	s.widthMu.Lock()
	defer s.widthMu.Unlock()
	if s.probeOverrides > 0 {
		return fmt.Errorf("run: an in-flight ephemeral probe holds a process-wide override (width or memory ceiling); retry after it finishes")
	}
	if s.widthClaims > 0 && resolved != s.widthJobs {
		return fmt.Errorf("run: an in-flight campaign with jobs=%d owns the process's oracle-parallelism width; request jobs=%d, a different width, after it finishes (or match its job count)", s.widthJobs, resolved)
	}
	if s.widthClaims == 0 {
		s.widthJobs = resolved
	}
	s.widthClaims++
	return nil
}

func (s *Server) releaseRunWidth() {
	s.widthMu.Lock()
	defer s.widthMu.Unlock()
	s.widthClaims--
}

// resolveJobs mirrors the run's own derivation (jobs<=0 means half the
// CPUs, floored at 1), so two spellings of the same effective width
// share one claim and the refusal names a count the agent can request.
func resolveJobs(jobs int) int {
	if jobs <= 0 {
		return max(1, runtime.NumCPU()/2)
	}
	return jobs
}

// claimProbeOverride admits an ephemeral probe's process-wide override
// (memory ceiling or width) only while no campaign is in flight, and
// blocks campaigns from starting until the probe releases - the same
// shared-state discipline as the width claim, closing the gap where a
// probe's deferred restore could land mid-campaign.
func (s *Server) claimProbeOverride() error {
	s.widthMu.Lock()
	defer s.widthMu.Unlock()
	if s.widthClaims > 0 {
		return fmt.Errorf("ephemeral: a run in flight owns the process's oracle width and memory ceiling; omit the overrides or retry after the run")
	}
	s.probeOverrides++
	return nil
}

func (s *Server) releaseProbeOverride() {
	s.widthMu.Lock()
	defer s.widthMu.Unlock()
	s.probeOverrides--
}

// New builds a server rooted at dir.
func New(dir string, opts ...Option) *Server {
	s := &Server{dir: dir}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures the server at construction.
type Option func(*Server)

// WithDynamicStateVouches installs the caller's reviewed dynamic-state
// vouch set on every tree the server loads: the loaded tree is shared
// across concurrent tool calls, so the set is a server input, never a
// per-call one.
func WithDynamicStateVouches(identities ...string) Option {
	return func(s *Server) {
		s.vouches = append([]string(nil), identities...)
	}
}

func (s *Server) update(ctx context.Context, path string, change func([]gomutant.Finding) ([]gomutant.Finding, error)) error {
	if s.updateDocument != nil {
		return s.updateDocument(ctx, path, change)
	}
	store, err := gomutant.OpenStore(path, s.dir)
	if err != nil {
		return err
	}
	return store.Update(ctx, change)
}

// Run serves MCP over stdio until the context ends.
func (s *Server) Run(ctx context.Context) error {
	return s.MCP().Run(ctx, &mcp.StdioTransport{})
}

// MCP builds the protocol server (REQ-mcp-tools).
// serverOptions builds the MCP server options - extracted so the
// keepalive configuration is a testable fact.
func serverOptions() *mcp.ServerOptions {
	return &mcp.ServerOptions{
		// The keepalive ping is the disconnect detector for in-flight
		// campaigns: a ping over a dead transport fails the write, and
		// the connection cancels every in-flight handler context - so a
		// client that died mid-run aborts the campaign within the
		// interval instead of measuring detached for its full duration
		// (REQ-mcp-liveness). A client that abandons a request while
		// its connection lives owes a cancellation notification per the
		// protocol; the ping cannot see intent.
		KeepAlive:    clientKeepAliveInterval,
		Instructions: guidanceOrientation(),
	}
}

// heartbeatInterval paces withHeartbeat's still-working notifications;
// a variable so the emission is testable without a 20-second test.
var heartbeatInterval = 20 * time.Second

// withHeartbeat runs fn while a bounded ticker tells a progress-token
// client the labeled stretch is still working - no compile, load, or
// oracle stretch stays silent when the client asked for progress
// (REQ-mcp-envelope). The caller passes its request's ONE notifier so
// every stretch shares one monotonically increasing progress counter -
// MCP requires the value to increase per token, and a fresh counter per
// stretch would regress it. A nil notifier (no token) is exactly fn.
func withHeartbeat[T any](ctx context.Context, notify func(string), label string, fn func(context.Context) (T, error)) (T, error) {
	if notify == nil {
		return fn(ctx)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		started := time.Now()
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				notify(fmt.Sprintf("still working: %s (%s elapsed)", label, time.Since(started).Round(time.Second)))
			}
		}
	}()
	return fn(ctx)
}

func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gomutant", Version: "v0"}, serverOptions())
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run",
		Description: guidanceDescription("run"),
	}, s.toolRun)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "discover",
		Description: guidanceDescription("discover"),
	}, s.toolDiscover)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "findings",
		Description: guidanceDescription("findings"),
	}, s.toolFindings)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "explain",
		Description: guidanceDescription("explain"),
	}, s.toolExplain)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_survivor",
		Description: guidanceDescription("attest_survivor"),
	}, s.toolAttest)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "prune",
		Description: guidanceDescription("prune"),
	}, s.toolPrune)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "retarget",
		Description: guidanceDescription("retarget"),
	}, s.toolRetarget)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ephemeral",
		Description: guidanceDescription("ephemeral"),
	}, s.toolEphemeral)
	// guidance serves embedded content and touches no tree state.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "guidance",
		Description: guidanceDescription("guidance"),
	}, s.toolGuidance)
	return srv
}

const defaultFindings = ".gomutant/findings.json"

// defaultCommandTimeoutSec bounds MCP tool work when the caller omits
// timeout_sec: typical MCP clients abandon a request within a few minutes,
// and a server that keeps working past its client's private deadline commits
// a result nobody receives. An explicit 0 still means unlimited.
const defaultCommandTimeoutSec = 300

func secondsDuration(name string, seconds int) (time.Duration, error) {
	const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
	if seconds < 0 || int64(seconds) > maxSeconds {
		return 0, fmt.Errorf("%s is outside the supported duration range", name)
	}
	return time.Duration(seconds) * time.Second, nil
}

// commandTimeout resolves an optional timeout_sec input: absent defaults to
// defaultCommandTimeoutSec, an explicit 0 means unlimited.
func commandTimeout(name string, seconds *int) (time.Duration, error) {
	if seconds == nil {
		return defaultCommandTimeoutSec * time.Second, nil
	}
	return secondsDuration(name, *seconds)
}

// capRunFindings builds the run response's finding rows under the
// envelope caps (REQ-mcp-envelope): a campaign multiplies every list;
// the document on disk carries the full set, so the response counts
// what it drops instead of inlining it. Candidate evidence is
// drill-down via the findings tool, not run payload. Each measured or
// cached row states its persistence layer (REQ-result-layers): whether
// the record is safe to stage is answered by the response, never by a
// second findings call after a run that rendered healthy counts while
// the store routed the record to the machine-local overlay.
func capRunFindings(findings []gomutant.Finding, layer func(gomutant.Finding) (string, string)) (rows []findingOut, omitted int) {
	const openCap = 20
	for _, f := range findings {
		if len(rows) == envelopeRowCap {
			omitted++
			continue
		}
		open := f.Open()
		omittedOpen := 0
		if len(open) > openCap {
			omittedOpen = len(open) - openCap
			open = open[:openCap]
		}
		row := findingOut{
			Symbol: f.Symbol, Labels: f.Labels,
			CandidateCount: f.CandidateCount, Generated: f.Generated,
			Mutants: f.Mutants, Killed: f.Killed, Discarded: f.Discarded,
			Attested: len(f.Attested), Open: open,
			OmittedOpen: omittedOpen,
			Cached:      f.Cached, Skipped: f.Skipped,
		}
		if f.Skipped == "" {
			row.Layer, row.LayerReason = layer(f)
		}
		rows = append(rows, row)
	}
	return rows, omitted
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
	Suggestion     string   `json:"suggestion"`
}

// guidanceListCap bounds a guidance row's nested lists - one row
// aggregates every target an unstable oracle set covers, the exact
// unauthored blow-up REQ-mcp-envelope refuses; the explain tool's
// per-group symbol cap is the precedent.
const guidanceListCap = 10

// contradictionOut is one shed attestation report (a drift serve's added
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
	*entries = append(*entries, guidanceOut{Targets: []string{g.Symbol}, UnstableTests: g.UnstableTests, Reason: g.Reason, Suggestion: g.Suggestion})
}

// runStreams routes the run's preparation and decision streams: with a
// progress token they ride notifications and leave the response, their
// totals remaining as counts; without one they stay inline, capped
// (REQ-mcp-envelope). lastPhase feeds the heartbeat.
type runStreams struct {
	out       *runOut
	notify    func(string)
	lastPhase *atomic.Value
}

const streamRowCap = 100

// analysisEventMessage renders an analysis event for the advisory
// notification channel; a non-empty detail (the per-subject
// analysis-unavailable provenance, the unlisted-toolchain notice)
// rides the same line after an em dash, matching the CLI face's
// rendering.
func analysisEventMessage(phase, pkg, detail string) string {
	message := "analysis " + phase
	if pkg != "" {
		message += " " + pkg
	}
	if detail != "" {
		message += " — " + detail
	}
	return message
}

func newRunStreams(out *runOut, notify func(string)) runStreams {
	var phase atomic.Value
	phase.Store("preparing")
	return runStreams{out: out, notify: notify, lastPhase: &phase}
}

func (r runStreams) decision(decision gomutant.RunDecision) {
	r.out.DecisionsCount++
	r.lastPhase.Store("executing mutants")
	if r.notify != nil {
		r.notify(decisionMessage(decision))
		return
	}
	if len(r.out.Decisions) < streamRowCap {
		r.out.Decisions = append(r.out.Decisions, decision)
	}
}

func (r runStreams) progress(event gomutant.PreparationEvent) {
	r.out.PreparationCount++
	r.lastPhase.Store("prepare " + string(event.Stage))
	if r.notify != nil {
		r.notify(preparationMessage(event))
		return
	}
	if len(r.out.Preparation) < streamRowCap {
		r.out.Preparation = append(r.out.Preparation, event)
	}
}

// progressNotifier returns a concurrency-safe sender of MCP progress
// notifications for req, or nil when the request carries no progress token.
// Delivery is advisory: a notification failure never fails the tool.
func progressNotifier(ctx context.Context, req *mcp.CallToolRequest) func(message string) {
	if req == nil || req.Session == nil || req.Params == nil {
		return nil
	}
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	var count atomic.Int64
	return func(message string) {
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Progress:      float64(count.Add(1)),
			Message:       message,
		})
	}
}

func preparationMessage(event gomutant.PreparationEvent) string {
	message := "prepare " + string(event.Stage)
	if event.Symbol != "" {
		message += " " + event.Symbol
	}
	if event.Package != "" {
		message += " " + event.Package
	}
	return message
}

func decisionMessage(decision gomutant.RunDecision) string {
	message := "decision " + decision.Action + " " + decision.Symbol
	if decision.Reason != "" {
		message += " (" + decision.Reason + ")"
	}
	if decision.Action == "measure" || decision.Candidates != 0 {
		message += fmt.Sprintf(", %d candidates", decision.Candidates)
	}
	return message
}

func (s *Server) findingsPath(override string) string {
	p := override
	if p == "" {
		p = defaultFindings
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.dir, filepath.FromSlash(p))
}

// localPath refuses a tree-relative input that escapes the server's dir —
// the surface is dir-bound, and an escaping ephemeral file would no-op in
// the overlay and read as a survivor.
func localPath(name, p string) error {
	if p == "" {
		return nil
	}
	drive := len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
	if !strings.Contains(p, `\`) && !path.IsAbs(p) && !drive && path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../") {
		return nil
	}
	return fmt.Errorf("%s %q escapes the tree", name, p)
}

func (s *Server) loadFindings(override string) ([]gomutant.Finding, error) {
	return s.loadFindingsContext(context.Background(), override)
}

func (s *Server) loadFindingsContext(ctx context.Context, override string) ([]gomutant.Finding, error) {
	store, err := gomutant.OpenStore(s.findingsPath(override), s.dir)
	if err != nil {
		return nil, err
	}
	findings, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	return findings, ctx.Err()
}

// selectionIn is the build-selection surface every tree-consuming tool
// shares: declared tags and a toolchain directive rewrite the tree's
// one frozen environment at load, so discovery, resolution, oracle
// spawns, and the measurement pins all see the same selection by
// construction (a //go:build-gated oracle measures exactly as an
// untagged one).
type selectionIn struct {
	Tags      []string `json:"tags,omitempty" jsonschema:"build tags for this call's selection (replaces any ambient GOFLAGS -tags); a go:build-gated symbol or oracle under the tags measures exactly as an untagged one"`
	Toolchain string   `json:"toolchain,omitempty" jsonschema:"GOTOOLCHAIN directive for this call's selection (e.g. go1.26.5); rides the toolchain measurement pin, so a different selection re-measures rather than serving across"`
}

// judged reports whether any judged-question input was given: the
// state filter and the selection (tags/toolchain) exist only to shape
// the freshness derivation, so either implies judge (vouches are
// server options, not per-call inputs, on this surface).
func (in findingsIn) judged() bool {
	return in.Judge || in.State != "" || len(in.Tags) > 0 || in.Toolchain != ""
}

func (s selectionIn) selection() gomutant.Selection {
	return gomutant.Selection{Tags: s.Tags, Toolchain: s.Toolchain}
}

type runIn struct {
	selectionIn
	TargetsPath       string   `json:"targets_path,omitempty" jsonschema:"path to a gomutant targets document; overrides discovery"`
	TargetsJSON       string   `json:"targets_json,omitempty" jsonschema:"an inline targets document, same formats as targets_path"`
	Changed           string   `json:"changed,omitempty" jsonschema:"target only symbols whose bodies differ from this git ref (requires git)"`
	Budget            int      `json:"budget,omitempty" jsonschema:"candidates per symbol; 0 means exhaustive"`
	TimeoutSec        *int     `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before the final findings commit after this many seconds; omitted means 300, and an explicit 0 means unlimited"`
	OracleTimeoutSec  int      `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
	Jobs              int      `json:"jobs,omitempty" jsonschema:"concurrent mutant runs; 0 means half the CPUs"`
	BracketPaths      []string `json:"bracket_paths,omitempty" jsonschema:"external surfaces the oracle legitimately reads (module-relative paths or absolute files; absolute directories and tool-excluded paths are refused); extends each spawn's observation bracket, carrying the caller's assertion the surface is mutation-free for the run"`
	ScratchNamespaces []string `json:"scratch_namespaces,omitempty" jsonschema:"in-module run-scratch namespaces DIR:PATTERN (DIR module-relative, PATTERN a single-component os.MkdirTemp-style name pattern): oracle scratch minted and removed inside a namespace stops recording per-run missing-arm noise, forfeiting exactly the appearance-pin of absence-probes the pattern matches; malformed declarations refuse before any measurement. Killed mutants never run test cleanup, so scratch helpers must enforce their own freshness (RemoveAll before MkdirAll) and expect permission-mangled residue from mutated code"`
	OracleMemoryMiB   *int64   `json:"oracle_memory_mib,omitempty" jsonschema:"memory ceiling per oracle process tree in MiB: absent or 0 derives RAM/(2 x jobs) floored at 1 GiB, -1 disables; a runaway-allocation mutant dies on its own ceiling as an ordinary kill instead of OOMing the host"`
	Staged            bool     `json:"staged,omitempty" jsonschema:"measure the git index snapshot: staged-but-uncommitted content counts clean and the finding records the index tree identity; unstaged drift over a measured target's inputs refuses that target"`
	Force             bool     `json:"force,omitempty" jsonschema:"re-measure even targets whose prior finding still covers the request; the pin spans the mutated symbol's body, every oracle test's source closure, and the observed runtime inputs (toolchain, build configuration, and the other measurement pins are always compared too), so new or changed oracle tests re-measure without force"`
	Findings          string   `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json), read and updated"`
	Packages          []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols           []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
}

type findingOut struct {
	Symbol         string              `json:"symbol"`
	Labels         []string            `json:"labels,omitempty"`
	CandidateCount int                 `json:"candidateCount"`
	Generated      int                 `json:"generated"`
	Mutants        int                 `json:"mutants"`
	Killed         int                 `json:"killed"`
	Discarded      int                 `json:"discarded"`
	Attested       int                 `json:"attested,omitempty"`
	Open           []gomutant.Survivor `json:"open,omitempty"`
	OmittedOpen    int                 `json:"omittedOpen,omitempty" jsonschema:"open survivors beyond the response cap; the findings tool serves the full set"`
	Cached         bool                `json:"cached,omitempty"`
	Skipped        string              `json:"skipped,omitempty"`
	Layer          string              `json:"layer,omitempty" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay; absent on skipped targets"`
	LayerReason    string              `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
}

type runOut struct {
	Summary                   gomutant.RunSummary         `json:"summary"`
	Document                  string                      `json:"document"`
	Findings                  []findingOut                `json:"findings"`
	OmittedFindings           int                         `json:"omittedFindings,omitempty" jsonschema:"finding rows beyond the response cap; the document carries the full set"`
	Guidance                  []guidanceOut               `json:"oracleGuidance,omitempty" jsonschema:"oracle-instability attributions aggregated per oracle set: targets sharing one unstable oracle share one entry"`
	OmittedGuidance           int                         `json:"omittedOracleGuidance,omitempty" jsonschema:"guidance rows beyond the response cap - counted, never silent"`
	Contradictions            []contradictionOut          `json:"attestationContradictions,omitempty" jsonschema:"attested survivors a drift serve's added or moved tests killed: each attestation was shed because evidence beats attestation, and the equivalence judgment wants re-review"`
	OmittedContradictions     int                         `json:"omittedAttestationContradictions,omitempty" jsonschema:"contradiction rows beyond the response cap; the findings tool serves every open survivor"`
	PropertyOracles           []string                    `json:"propertyOracles,omitempty" jsonschema:"property-runtime prerequisite statements per oracle package: what the run pinned itself (rapid: seed and reproducer files), or what the caller must ensure (gopter: an in-suite fixed seed) for reproducible verdicts"`
	OmittedPropertyOracles    int                         `json:"omittedPropertyOracles,omitempty" jsonschema:"property-oracle rows beyond the response cap"`
	AttestationSheds          []string                    `json:"attestationSheds,omitempty" jsonschema:"dispositions shed with the cause named - the mutation domain moved, the site content under the position changed, or the attested survivor is no longer reported - re-review and re-attest if genuinely equivalent"`
	OmittedAttestationSheds   int                         `json:"omittedAttestationSheds,omitempty" jsonschema:"shed rows beyond the response cap; every shed mutant is one of the document's open survivors"`
	AttestationCarries        []string                    `json:"attestationCarries,omitempty" jsonschema:"dispositions carried across moved measurement pins: the mutated source is unchanged and the mutant survived re-execution, so the equivalence reasoning rides - auditable acceptances, no action needed"`
	OmittedAttestationCarries int                         `json:"omittedAttestationCarries,omitempty" jsonschema:"carry rows beyond the response cap; the document's attestations carry the reasoning"`
	Promoted                  int                         `json:"promoted,omitempty" jsonschema:"records this run carried from the machine-local overlay into the committed findings document - the document changed, commit it"`
	MachineLocalOnly          int                         `json:"machineLocalOnly,omitempty" jsonschema:"records this run routed to the machine-local overlay - the repo findings document gains nothing from them until their per-record disqualifiers clear; the capped findings list may omit some, this count never does"`
	Residue                   []gomutant.Residue          `json:"residue,omitempty"`
	OmittedResidue            int                         `json:"omittedResidue,omitempty"`
	Preparation               []gomutant.PreparationEvent `json:"preparation,omitempty" jsonschema:"absent when a progress token streamed the events; preparationCount still totals them"`
	PreparationCount          int                         `json:"preparationCount"`
	Decisions                 []gomutant.RunDecision      `json:"decisions,omitempty" jsonschema:"absent when a progress token streamed the decisions; decisionsCount still totals them"`
	DecisionsCount            int                         `json:"decisionsCount"`
	Note                      string                      `json:"note,omitempty" jsonschema:"set when the run measured nothing (names the input that selected zero targets and the next step) or when a whole-tree reconcile dropped records whose targets left the code"`
}

// envelopeRowCap is the one row bound every capped response list
// shares (REQ-mcp-envelope); per-row nested lists carry their own
// tighter bounds.
const envelopeRowCap = 50

// capRows bounds a response list at the envelope cap with the remainder
// counted (REQ-mcp-envelope); the findings document carries every full
// set a capped row points at. The returned slice is capacity-clipped so
// a later append can never scribble over the caller's retained full
// list.
func capRows[T any](rows []T) ([]T, int) {
	if len(rows) <= envelopeRowCap {
		return rows, 0
	}
	return rows[:envelopeRowCap:envelopeRowCap], len(rows) - envelopeRowCap
}

// selectionEmptiedNote names the input that emptied a target selection
// so the caller's next step is a decision, not a diagnosis
// (REQ-mcp-envelope). Shared by run and discover: one discrimination,
// one wording. Filters never appear here — filtering an already-empty
// selection is skipped (nothing exists for filters to drop, so blaming
// them would teach the wrong next step), and filters that empty a
// non-empty selection refuse inside FilterTargetsContext with their
// own teaching error.
func selectionEmptiedNote(targetsDoc bool, changed string) string {
	switch {
	case targetsDoc:
		return "the targets document selected zero effective targets; discover previews a document's effective targets"
	case changed != "":
		return fmt.Sprintf("no targets changed vs %s; omit changed to select the whole tree", changed)
	default:
		return "the tree has no mutation targets"
	}
}

// droppedSymbols counts the symbols a reconcile removed — a symbol-set
// difference, so a document hand-edited into duplicate records for one
// symbol cannot overcount the drop.
func droppedSymbols(current, merged []gomutant.Finding) int {
	kept := make(map[string]bool, len(merged))
	for _, m := range merged {
		kept[m.Symbol] = true
	}
	seen := map[string]bool{}
	dropped := 0
	for _, c := range current {
		if !kept[c.Symbol] && !seen[c.Symbol] {
			seen[c.Symbol] = true
			dropped++
		}
	}
	return dropped
}

// targetSelection is one resolved target-selection request.
type targetSelection struct {
	targets   []gomutant.Target
	residue   []gomutant.Residue
	wholeTree bool
}

// selectTargets resolves a selection request through the one preamble
// run and discover share: the exclusive-forms refusal, the source
// dispatch (targets document, inline document, changed ref, or whole
// tree), and the filter walk — whose empty-selection discrimination
// lives in the library, so the callers' zero-target notes name the
// true emptier (REQ-target-filtering, REQ-mcp-envelope).
func (s *Server) selectTargets(ctx context.Context, tree *gomutant.Tree, targetsPath, targetsJSON, changed string, packages, symbols []string) (targetSelection, error) {
	var sel targetSelection
	forms := 0
	if targetsPath != "" {
		forms++
	}
	if targetsJSON != "" {
		forms++
	}
	if changed != "" {
		forms++
	}
	if forms > 1 {
		return sel, fmt.Errorf("give targets_path, targets_json, or changed, at most one")
	}
	var err error
	switch {
	case targetsPath != "":
		if err := localPath("targets_path", targetsPath); err != nil {
			return sel, err
		}
		data, err := contextio.ReadFile(ctx, filepath.Join(s.dir, filepath.FromSlash(targetsPath)))
		if err != nil {
			return sel, err
		}
		if err := ctx.Err(); err != nil {
			return sel, err
		}
		if sel.targets, err = gomutant.LoadTargetsContext(ctx, data); err != nil {
			return sel, err
		}
	case targetsJSON != "":
		if sel.targets, err = gomutant.LoadTargetsContext(ctx, []byte(targetsJSON)); err != nil {
			return sel, err
		}
	case changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, s.dir, changed)
		if err != nil {
			return sel, err
		}
		sel.targets, sel.residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, s.dir, changed, p)
		})
		if err != nil {
			return sel, err
		}
	default:
		if sel.targets, err = tree.DiscoverContext(ctx); err != nil {
			return sel, err
		}
		sel.wholeTree = true
	}
	if sel.targets, err = tree.FilterTargetsContext(ctx, sel.targets, packages, symbols); err != nil {
		return sel, err
	}
	if len(packages) != 0 || len(symbols) != 0 {
		sel.wholeTree = false
	}
	return sel, nil
}

// capAdvisories bounds the run response's five advisory lists and each
// guidance row's nested lists at the envelope caps with counted
// remainders (REQ-mcp-envelope), returning the FULL shed list so the
// drift error's own exemplar-bounded fold never shrinks with the
// response.
func (out *runOut) capAdvisories() (fullSheds []string) {
	fullSheds = out.AttestationSheds
	for i := range out.Guidance {
		if n := len(out.Guidance[i].Targets); n > guidanceListCap {
			out.Guidance[i].OmittedTargets = n - guidanceListCap
			out.Guidance[i].Targets = out.Guidance[i].Targets[:guidanceListCap:guidanceListCap]
		}
		if n := len(out.Guidance[i].UnstableTests); n > guidanceListCap {
			out.Guidance[i].OmittedTests = n - guidanceListCap
			out.Guidance[i].UnstableTests = out.Guidance[i].UnstableTests[:guidanceListCap:guidanceListCap]
		}
	}
	out.Guidance, out.OmittedGuidance = capRows(out.Guidance)
	out.Contradictions, out.OmittedContradictions = capRows(out.Contradictions)
	out.PropertyOracles, out.OmittedPropertyOracles = capRows(out.PropertyOracles)
	out.AttestationSheds, out.OmittedAttestationSheds = capRows(out.AttestationSheds)
	out.AttestationCarries, out.OmittedAttestationCarries = capRows(out.AttestationCarries)
	return fullSheds
}

func (s *Server) toolRun(ctx context.Context, req *mcp.CallToolRequest, in runIn) (result *mcp.CallToolResult, out runOut, err error) {
	// The oracle-parallelism width is process state every in-flight
	// campaign shares (REQ-exec-oracle-parallelism): concurrent runs
	// against different documents are legal, but a second campaign
	// installing a NARROWER width would rewrite the first campaign's
	// later spawn widths downward, splitting those oracles' recorded
	// environment from the campaign's evidence environment (degrade or
	// re-measure - fail-safe, and refused here instead). Symmetric with
	// the ephemeral probe-override refusal: the width owner is the
	// first in-flight campaign's resolved job count.
	if err := s.claimRunWidth(in.Jobs); err != nil {
		return nil, out, err
	}
	defer s.releaseRunWidth()
	timeout, err := commandTimeout("timeout_sec", in.TimeoutSec)
	if err != nil {
		return nil, out, err
	}
	oracleTimeout, err := secondsDuration("oracle_timeout_sec", in.OracleTimeoutSec)
	if err != nil {
		return nil, out, err
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
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	loading := gomutant.PreparationEvent{Stage: gomutant.PreparationLoading}
	out.PreparationCount++
	if notify != nil {
		notify(preparationMessage(loading))
	} else {
		out.Preparation = append(out.Preparation, loading)
	}
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		return nil, out, err
	}
	var targets []gomutant.Target
	wholeTree := false
	sel, err := s.selectTargets(ctx, tree, in.TargetsPath, in.TargetsJSON, in.Changed, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	targets, wholeTree = sel.targets, sel.wholeTree
	out.Residue = sel.residue
	prior, err := s.loadFindingsContext(ctx, in.Findings)
	if err != nil {
		return nil, out, err
	}
	if out.Residue, err = tree.OracleClosureSignpostContext(ctx, out.Residue, prior, targets); err != nil {
		return nil, out, err
	}
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		out.Residue, out.OmittedResidue = capRows(out.Residue)
		out.Document = s.findingsPath(in.Findings)
		out.Note = selectionEmptiedNote(in.TargetsPath != "" || in.TargetsJSON != "", in.Changed)
		if wholeTree {
			dropped := 0
			err := s.update(ctx, out.Document, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				merged := gomutant.MergeWholeFindings(current, nil, nil)
				dropped = droppedSymbols(current, merged)
				return merged, nil
			})
			if err != nil {
				return nil, out, err
			}
			// A whole-tree reconcile against zero targets drops every
			// record whose target left the code - a document write the
			// response must own, never bury in an empty success.
			if dropped > 0 {
				out.Note += fmt.Sprintf("; the whole-tree reconcile dropped %d record(s) whose targets left the code", dropped)
			}
		}
		return nil, out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	// The campaign lock spans measurement through the final merge: a
	// second campaign against the same document refuses immediately
	// instead of interleaving (REQ-exec-exclusivity).
	releaseCampaign, err := gomutant.AcquireCampaignLock(s.findingsPath(in.Findings))
	if err != nil {
		return nil, out, err
	}
	defer releaseCampaign()
	streams := newRunStreams(&out, notify)
	var commitSheds []gomutant.AttestationShed
	// The run-start snapshot of dispositions per symbol makes the merge
	// graft pin-correct, and the post-merge rows are the response truth:
	// what the harness reads is what the document holds
	// (REQ-attest-survivor, REQ-mcp-findings-doc).
	attestSnapshot := map[string][]gomutant.Attestation{}
	for _, f := range prior {
		attestSnapshot[f.Symbol] = append([]gomutant.Attestation(nil), f.Attested...)
	}
	postMerge := map[string]gomutant.Finding{}
	contradicted := map[string]bool{}
	// A shed is recorded the moment its strip persists: the incremental
	// commit survives an aborted run (that is its purpose), so a shed
	// buffered for the epilogue is silently dropped exactly when the
	// document kept the stripped record (REQ-attest-survivor's "loudly,
	// in every mode"). Delivery on abort: the SDK discards the typed
	// output when the handler errors, so the abort returns below ride
	// the recorded sheds on the error itself (the drift-refusal
	// precedent); a client that sent a progress token additionally hears
	// each one as it lands. The epilogue appends only what no commit
	// recorded. The library emits contradictions before their target's
	// commit, so the filter is complete at record time. recordShed is
	// never called under the findings-document lock - the notification
	// writes to the client transport, and a slow client must not extend
	// the lock hold.
	recordedSheds := map[string]bool{}
	recordShed := func(d gomutant.AttestationShed) {
		key := d.Symbol + "\x00" + d.Position + "\x00" + d.Operator
		if recordedSheds[key] || contradicted[key] {
			return
		}
		recordedSheds[key] = true
		line := fmt.Sprintf("%s %s %s - %s", d.Symbol, d.Position, d.Operator, d.Reason)
		out.AttestationSheds = append(out.AttestationSheds, line)
		if notify != nil {
			notify("attestation shed: " + line)
		}
	}
	priorLayer := map[string]string{}
	scratchNamespaces, err := gomutant.ParseScratchNamespaces(in.ScratchNamespaces)
	if err != nil {
		return nil, out, err
	}
	exemptions, err := gomutant.LoadExemptions(gomutant.ExemptionsPathFor(s.findingsPath(in.Findings)))
	if err != nil {
		return nil, out, err
	}
	options := gomutant.Options{
		Budget:            in.Budget,
		OracleTimeout:     oracleTimeout,
		Jobs:              in.Jobs,
		Force:             in.Force,
		BracketPaths:      in.BracketPaths,
		ScratchNamespaces: scratchNamespaces,
		Exemptions:        exemptions,
		Staged:            in.Staged,
		OwnWrites:         gomutant.RunOwnWrites(s.findingsPath(in.Findings)),
		OracleMemoryBytes: mcpOracleMemoryBytes(in.OracleMemoryMiB),
		Guidance:          func(g gomutant.OracleGuidance) { appendGuidance(&out.Guidance, g) },
		Contradiction: func(c gomutant.AttestationContradiction) {
			contradicted[c.Symbol+"\x00"+c.Position+"\x00"+c.Operator] = true
			out.Contradictions = append(out.Contradictions, contradictionOut{
				Symbol: c.Symbol, Position: c.Position, Operator: c.Operator, Killer: c.Killer, Reason: c.Reason,
			})
		},
		AttestationSiteShed: func(d gomutant.AttestationShed) {
			commitSheds = append(commitSheds, d)
			recordShed(d)
		},
		AttestationCarried: func(c gomutant.AttestationCarry) {
			line := fmt.Sprintf("%s %s %s - measurement pins moved; the mutated source is unchanged and the mutant survived re-execution", c.Symbol, c.Position, c.Operator)
			out.AttestationCarries = append(out.AttestationCarries, line)
			if notify != nil {
				notify("attestation carried: " + line)
			}
		},
		PropertyOracle: func(n gomutant.PropertyOracleNote) {
			out.PropertyOracles = append(out.PropertyOracles, fmt.Sprintf("%s %s: %s", n.Package, n.Runtime, n.Note))
		},
		Prior:    prior,
		Decision: streams.decision,
		Progress: streams.progress,
		// Each finished target commits under the same document lock the final
		// merge takes, so an interrupted run keeps its completed targets; the
		// final merge below remains the authority (REQ-exec-cancellation).
		Commit: func(finding gomutant.Finding) error {
			var dropped []gomutant.AttestationShed
			err := s.update(ctx, s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
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
			// Recorded after the update returns: the strip persisted, and
			// the client notification must not run under the document lock.
			commitSheds = append(commitSheds, dropped...)
			for _, d := range dropped {
				recordShed(d)
			}
			return nil
		},
	}
	if notify != nil {
		options.AnalysisEvent = func(phase, pkg, detail string) {
			notify(analysisEventMessage(phase, pkg, detail))
		}
		// Execution-phase progress joins the same advisory notification
		// channel (REQ-exec-run-status's advisory classes): phase,
		// window position, and exact campaign tallies.
		options.Executing = func(e gomutant.ExecutionEvent) {
			if e.Phase == "confirmation-flip" {
				// The demotion carries its payload on every face: a
				// provisional kill re-scored survivor names its mutant
				// and withdrawn killer (REQ-exec-run-status).
				notify(fmt.Sprintf("confirmation FLIP: %s %s - provisional kill by %s re-scored survivor on serial re-run", e.Symbol, e.FlipPosition, e.FlipKiller))
				return
			}
			message := fmt.Sprintf("%s target %d/%d %s candidates %d/%d", e.Phase, e.TargetIndex, e.TargetCount, e.Symbol, e.CandidatesDone, e.CandidatesTotal)
			if e.ConfirmationsTotal > 0 {
				message += fmt.Sprintf(" confirmations %d/%d", e.ConfirmationsDone, e.ConfirmationsTotal)
			}
			if e.ConfirmationMode != "" {
				// The gate state rides every face: the disarmed stride
				// must be distinguishable from the armed one for MCP
				// operators too (REQ-exec-run-status).
				message += " mode=" + e.ConfirmationMode
			}
			notify(message)
		}
	}
	// The heartbeat keeps long compile and execution stretches audible
	// under the client's deadline: no phase goes silent longer than the
	// cadence while a token listens (REQ-mcp-envelope).
	if notify != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			started := time.Now()
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					notify(fmt.Sprintf("still working: %s (%s elapsed)", streams.lastPhase.Load(), time.Since(started).Round(time.Second)))
				}
			}
		}()
	}
	findings, err := tree.Run(ctx, targets, options)
	var drift *gomutant.TreeDriftError
	if err != nil && !errors.As(err, &drift) {
		return nil, out, shedsRidingAbort(err, out.AttestationSheds)
	}
	if err := ctx.Err(); err != nil {
		return nil, out, shedsRidingAbort(err, out.AttestationSheds)
	}
	// The final merge runs before anything renders: the response reads
	// the rows the document actually holds - a disposition recorded
	// concurrently between a symbol's incremental commit and the end of
	// the run is in both or in neither (REQ-mcp-findings-doc).
	var attestationSheds []gomutant.AttestationShed
	reconcileDropped := 0
	err = s.update(ctx, s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var merged []gomutant.Finding
		if wholeTree {
			merged, attestationSheds = gomutant.MergeWholeFindingsShedAgainst(current, findings, targets, attestSnapshot)
			reconcileDropped = droppedSymbols(current, merged)
		} else {
			merged, attestationSheds = gomutant.MergeFindingsShedAgainst(current, findings, attestSnapshot)
		}
		for _, m := range merged {
			if _, ran := postMerge[m.Symbol]; ran {
				postMerge[m.Symbol] = m
			}
		}
		return merged, nil
	})
	// From here every error exit owns the reconcile's PERSISTED drop:
	// the SDK discards the response object on error, so the count folds
	// into the error text exactly as sheds do (REQ-mcp-envelope).
	withDrop := func(err error) error {
		if reconcileDropped == 0 {
			return err
		}
		return fmt.Errorf("%w — additionally, the whole-tree reconcile had already dropped %d record(s) whose targets left the code (persisted)", err, reconcileDropped)
	}
	if err != nil {
		return nil, out, withDrop(shedsRidingAbort(err, out.AttestationSheds))
	}
	rendered := gomutant.RenderedFindings(findings, postMerge)
	out.Summary = gomutant.SummarizeRun(rendered)
	runStore, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		return nil, out, withDrop(shedsRidingAbort(err, out.AttestationSheds))
	}
	for _, f := range prior {
		priorLayer[f.Symbol], _ = runStore.Layer(f)
	}
	out.Findings, out.OmittedFindings = capRunFindings(rendered, runStore.Layer)
	out.Residue, out.OmittedResidue = capRows(out.Residue)
	// A shed disposition is surfaced once, never silently dropped
	// (REQ-attest-survivor): the first report wins - a shed the
	// incremental commit already recorded, or a mutant whose fate the
	// contradiction row already told, is not retold with the merge
	// layer's vaguer reason.
	for _, d := range gomutant.DedupeAttestationSheds(append(append([]gomutant.AttestationShed(nil), commitSheds...), attestationSheds...)) {
		key := d.Symbol + "\x00" + d.Position + "\x00" + d.Operator
		if contradicted[key] || recordedSheds[key] {
			continue
		}
		out.AttestationSheds = append(out.AttestationSheds, fmt.Sprintf("%s %s %s - %s", d.Symbol, d.Position, d.Operator, d.Reason))
	}
	// A record this run carried from the machine-local overlay into the
	// committed document is a state change git does not see until
	// committed, so the response says it happened (REQ-mcp-findings-doc).
	for symbol, merged := range postMerge {
		if layer, _ := runStore.Layer(merged); layer == "repo" && priorLayer[symbol] == "local" {
			out.Promoted++
		}
	}
	// The aggregate machine-local count survives the findings-list cap:
	// a large all-local campaign must state its non-persistence even
	// when the capped rows cannot carry every disqualifier
	// (REQ-result-local-signpost).
	for _, f := range rendered {
		if f.Skipped != "" {
			continue
		}
		if layer, _ := runStore.Layer(f); layer == "local" {
			out.MachineLocalOnly++
		}
	}
	out.Document = s.findingsPath(in.Findings)
	// The advisory lists cap like every row surface; the drift error
	// still folds over the FULL shed list, capped by its own exemplar
	// bound - a capped response list must not shrink what the error
	// names (REQ-mcp-envelope).
	// Every whole-tree reconcile's document write is owned by the
	// response, not only the zero-target one: dropped records are a
	// state change git does not show until committed (REQ-mcp-envelope).
	if reconcileDropped > 0 {
		out.Note = fmt.Sprintf("the whole-tree reconcile dropped %d record(s) whose targets left the code", reconcileDropped)
	}
	fullSheds := out.capAdvisories()
	// A drift-refused campaign persists its completed findings and still
	// errors: the client never reads a partial campaign as success
	// (REQ-exec-quiescence). The SDK renders only the error text on
	// failure, so consequences already stripped from the document -
	// attestation sheds - fold into it via driftError: surfaced once,
	// never silently dropped (REQ-attest-survivor).
	if drift != nil {
		return nil, out, withDrop(driftError(drift, fullSheds))
	}
	return nil, out, nil
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

type discoverIn struct {
	selectionIn
	TargetsPath string   `json:"targets_path,omitempty" jsonschema:"path to a targets document; overrides discovery"`
	TargetsJSON string   `json:"targets_json,omitempty" jsonschema:"inline targets document; overrides discovery"`
	Changed     string   `json:"changed,omitempty" jsonschema:"changed-scope vs this git ref; empty means the whole tree"`
	Packages    []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols     []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
	Detail      bool     `json:"detail,omitempty" jsonschema:"return every target, oracle-set, and residue row; default caps each list at 50 with the remainder counted"`
}

type discoverTarget struct {
	Symbol         string   `json:"symbol" jsonschema:"fully qualified target symbol"`
	OracleSet      int      `json:"oracleSet" jsonschema:"zero-based id that references one entry in the top-level oracleSets array"`
	Labels         []string `json:"labels,omitempty" jsonschema:"sorted opaque labels carried unchanged from the target"`
	OracleExplicit bool     `json:"oracleExplicit,omitempty" jsonschema:"whether the referenced oracle was explicitly supplied rather than package-derived"`
	Skipped        string   `json:"skipped,omitempty" jsonschema:"reason this target cannot be measured, when applicable"`
}

type discoverOracleSet struct {
	ID     int      `json:"id" jsonschema:"zero-based oracle-set id referenced by targets[].oracleSet"`
	Oracle []string `json:"oracle" jsonschema:"sorted fully qualified test symbols forming this exact effective oracle"`
}

type discoverOut struct {
	TargetCount       int                 `json:"targetCount" jsonschema:"effective targets after filtering; leads the response so a campaign's scale reads before any row"`
	SkippedCount      int                 `json:"skippedCount,omitempty" jsonschema:"targets carrying a skip reason"`
	ResidueCount      int                 `json:"residueCount,omitempty" jsonschema:"changed-but-untargeted paths"`
	OracleSets        []discoverOracleSet `json:"oracleSets" jsonschema:"canonical exact oracle sets assigned in first-target order; capped beside the target rows - sets beyond the cap are referenced only by omitted targets"`
	Targets           []discoverTarget    `json:"targets" jsonschema:"ordered effective targets whose oracleSet references oracleSets[].id; capped at 50 unless detail=true"`
	OmittedTargets    int                 `json:"omittedTargets,omitempty" jsonschema:"target rows beyond the cap; set detail=true for the full set"`
	OmittedOracleSets int                 `json:"omittedOracleSets,omitempty" jsonschema:"oracle sets beyond the cap; set detail=true for the full set"`
	Residue           []gomutant.Residue  `json:"residue,omitempty"`
	OmittedResidue    int                 `json:"omittedResidue,omitempty"`
	Note              string              `json:"note,omitempty" jsonschema:"set when discovery selected zero targets: names the input that emptied the selection and the next step"`
}

func (s *Server) toolDiscover(ctx context.Context, req *mcp.CallToolRequest, in discoverIn) (*mcp.CallToolResult, discoverOut, error) {
	var out discoverOut
	notify := progressNotifier(ctx, req)
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		return nil, out, err
	}
	sel, err := s.selectTargets(ctx, tree, in.TargetsPath, in.TargetsJSON, in.Changed, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	targets := sel.targets
	out.Residue = sel.residue
	descriptions, err := tree.DescribeTargetsContext(ctx, targets)
	if err != nil {
		return nil, out, err
	}
	out.OracleSets, out.Targets = compactTargetDescriptions(descriptions)
	out.TargetCount = len(out.Targets)
	for _, target := range out.Targets {
		if target.Skipped != "" {
			out.SkippedCount++
		}
	}
	out.ResidueCount = len(out.Residue)
	// An empty selection is an answer, not silence: the note names the
	// input that emptied it, one discrimination shared with run
	// (REQ-mcp-envelope).
	if out.TargetCount == 0 {
		out.Note = selectionEmptiedNote(in.TargetsPath != "" || in.TargetsJSON != "", in.Changed)
	}
	out.capUnlessDetail(in.Detail)
	return nil, out, nil
}

// capUnlessDetail bounds the discovery row lists at the envelope cap
// with counted remainders unless the caller opted into the full set
// (REQ-mcp-envelope). Oracle sets are assigned in first-target order,
// so every retained target's set id stays within the same cap that
// bounds the target rows.
func (out *discoverOut) capUnlessDetail(detail bool) {
	if detail {
		return
	}
	out.Targets, out.OmittedTargets = capRows(out.Targets)
	out.OracleSets, out.OmittedOracleSets = capRows(out.OracleSets)
	out.Residue, out.OmittedResidue = capRows(out.Residue)
}

func compactTargetDescriptions(descriptions []gomutant.TargetDescription) ([]discoverOracleSet, []discoverTarget) {
	sets := make([]discoverOracleSet, 0)
	setByKey := map[string]int{}
	targets := make([]discoverTarget, 0, len(descriptions))
	for _, description := range descriptions {
		var key strings.Builder
		for _, oracle := range description.Oracle {
			fmt.Fprintf(&key, "%d:", len(oracle))
			key.WriteString(oracle)
		}
		id, ok := setByKey[key.String()]
		if !ok {
			id = len(sets)
			setByKey[key.String()] = id
			sets = append(sets, discoverOracleSet{ID: id, Oracle: description.Oracle})
		}
		targets = append(targets, discoverTarget{
			Symbol: description.Symbol, OracleSet: id, Labels: description.Labels,
			OracleExplicit: description.OracleExplicit, Skipped: description.Skipped,
		})
	}
	return sets, targets
}

type findingsIn struct {
	selectionIn
	Label    string `json:"label,omitempty" jsonschema:"show only findings carrying this label"`
	State    string `json:"state,omitempty" jsonschema:"show only findings in this judged state: current, stale, unverifiable, or detached (implies judge=true)"`
	Judge    bool   `json:"judge,omitempty" jsonschema:"re-derive each record's freshness state against the current tree - minutes-class on large documents; a state filter or a tags/toolchain selection implies it; the default reports recorded facts with state 'recorded' and loads no tree"`
	Symbol   string `json:"symbol,omitempty" jsonschema:"show only the finding for this mutated symbol"`
	Detail   bool   `json:"detail,omitempty" jsonschema:"full rows - operator tables, open survivors, attested dispositions, candidate evidence; the default is the bounded summary (one row per record: symbol, state, layer, open and attested counts)"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

// findingSummary is the bounded default row: enough to triage - what
// record, what state, which layer, how much is open - with the full
// lists behind detail (REQ-mcp-envelope, REQ-result-inspection).
type findingSummary struct {
	Symbol   string                `json:"symbol"`
	State    gomutant.FindingState `json:"state"`
	Reason   string                `json:"reason,omitempty"`
	Layer    string                `json:"layer"`
	Open     int                   `json:"open"`
	Attested int                   `json:"attested"`
}

type inspectedFinding struct {
	Symbol         string                       `json:"symbol"`
	Labels         []string                     `json:"labels,omitempty"`
	State          gomutant.FindingState        `json:"state"`
	Reason         string                       `json:"reason,omitempty"`
	Layer          string                       `json:"layer" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay"`
	LayerReason    string                       `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
	CandidateCount int                          `json:"candidateCount"`
	Generated      int                          `json:"generated"`
	Mutants        int                          `json:"mutants"`
	Killed         int                          `json:"killed"`
	Discarded      int                          `json:"discarded"`
	Operators      []gomutant.OperatorSummary   `json:"operators"`
	Open           []gomutant.Survivor          `json:"open"`
	Attested       []gomutant.Attestation       `json:"attested"`
	Candidates     []gomutant.CandidateEvidence `json:"candidateEvidence,omitempty"`
}

type findingsOut struct {
	Summary                      []findingSummary                `json:"summary,omitempty" jsonschema:"the bounded default: one row per record"`
	Findings                     []inspectedFinding              `json:"findings,omitempty" jsonschema:"full rows, only under detail"`
	Omitted                      int                             `json:"omitted,omitempty" jsonschema:"rows beyond the cap - the document on disk always carries the full set"`
	RepoCommittable              int                             `json:"repoCommittable" jsonschema:"records portable enough for the committed findings document"`
	LocalOnly                    int                             `json:"localOnly" jsonschema:"records held in the machine-local overlay a reviewer would not inherit"`
	Document                     string                          `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	Note                         string                          `json:"note,omitempty" jsonschema:"set when there are no rows: says whether the document is empty or the filters matched nothing, and the next step"`
	EphemeralAttestations        []gomutant.EphemeralAttestation `json:"ephemeralAttestations,omitempty" jsonschema:"committed ephemeral-equivalence attestations beside the document - judged-equivalent manual probes with their edit digests and reasoning; capped, the record on disk carries the full set"`
	OmittedEphemeralAttestations int                             `json:"omittedEphemeralAttestations,omitempty" jsonschema:"attestation rows beyond the response cap - counted, never silent"`
}

func (s *Server) toolFindings(ctx context.Context, req *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, findingsOut, error) {
	out := findingsOut{Document: s.findingsPath(in.Findings)}
	// The committed ephemeral-equivalence record rides the inspection,
	// independent of the finding rows (REQ-result-ephemeral-attest).
	if atts, err := gomutant.LoadEphemeralAttestations(gomutant.EphemeralAttestationsPathFor(out.Document)); err != nil {
		return nil, out, err
	} else if len(atts) > guidanceListCap {
		out.EphemeralAttestations = atts[:guidanceListCap]
		out.OmittedEphemeralAttestations = len(atts) - guidanceListCap
	} else {
		out.EphemeralAttestations = atts
	}
	switch in.State {
	case "", string(gomutant.FindingCurrent), string(gomutant.FindingStale), string(gomutant.FindingUnverifiable), string(gomutant.FindingDetached):
	default:
		return nil, out, fmt.Errorf("unknown state %q (current, stale, unverifiable, detached)", in.State)
	}
	store, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return nil, out, err
	}
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	// Zero rows is an answer with two different next steps - measure
	// first, or widen the filters - so the response says which
	// (REQ-mcp-envelope).
	if len(all) == 0 {
		out.Note = "no findings recorded at " + out.Document + " - run measures the tree first"
		return nil, out, nil
	}
	matched := make([]gomutant.Finding, 0, len(all))
	for _, finding := range all {
		if in.Label != "" && !containsLabel(finding.Labels, in.Label) {
			continue
		}
		if in.Symbol != "" && finding.Symbol != in.Symbol {
			continue
		}
		matched = append(matched, finding)
	}
	if len(matched) == 0 {
		out.Note = fmt.Sprintf("the label/symbol filters matched none of %d recorded finding(s); drop them to list the document", len(all))
		return nil, out, nil
	}
	notify := progressNotifier(ctx, req)
	// The default path loads no tree at all: the document's recorded
	// facts answer without one, and the five-minute field inspections
	// were freshness re-derivation, never parsing.
	judge := in.judged()
	var tree *gomutant.Tree
	if judge {
		tree, err = withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
		if err != nil {
			return nil, out, err
		}
	}
	// The inspection stretch announces itself once and rides the
	// heartbeat: freshness judging over a large document is
	// minutes-class work, and a silent stretch reads as a hang
	// (REQ-mcp-envelope). Rows build one inspection at a time so
	// summary reads never retain per-candidate evidence for the whole
	// document.
	if notify != nil && judge {
		notify(fmt.Sprintf("inspecting %d record(s)", len(matched)))
	}
	stretch := fmt.Sprintf("reading %d record(s)", len(matched))
	if judge {
		stretch = fmt.Sprintf("inspecting %d record(s)", len(matched))
	}
	rows, err := withHeartbeat(ctx, notify, stretch, func(ctx context.Context) (findingsOut, error) {
		var res findingsOut
		for _, finding := range matched {
			if err := ctx.Err(); err != nil {
				return res, err
			}
			inspection := gomutant.RecordedInspection(finding)
			if tree != nil {
				judged, err := tree.InspectFindingContext(ctx, finding)
				if err != nil {
					return res, err
				}
				inspection = judged
			}
			if in.State != "" && string(inspection.State) != in.State {
				continue
			}
			layer, layerReason := store.Layer(finding)
			if layer == "repo" {
				res.RepoCommittable++
			} else {
				res.LocalOnly++
			}
			if !in.Detail {
				res.Summary = append(res.Summary, findingSummary{
					Symbol: finding.Symbol, State: inspection.State, Reason: inspection.Reason,
					Layer: layer, Open: len(finding.Open()), Attested: len(finding.AttestedDispositions()),
				})
				continue
			}
			labels := append([]string(nil), finding.Labels...)
			sort.Strings(labels)
			res.Findings = append(res.Findings, inspectedFinding{
				Symbol: finding.Symbol, Labels: labels, State: inspection.State, Reason: inspection.Reason,
				Layer: layer, LayerReason: layerReason,
				CandidateCount: finding.CandidateCount, Generated: finding.Generated,
				Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
				Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
				Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
				Candidates: inspection.CandidateEvidence,
			})
		}
		return res, nil
	})
	if err != nil {
		return nil, out, err
	}
	out.RepoCommittable, out.LocalOnly = rows.RepoCommittable, rows.LocalOnly
	out.Summary, out.Findings = rows.Summary, rows.Findings
	sort.Slice(out.Summary, func(i, j int) bool { return out.Summary[i].Symbol < out.Summary[j].Symbol })
	sort.Slice(out.Findings, func(i, j int) bool { return out.Findings[i].Symbol < out.Findings[j].Symbol })
	// The state filter drops rows during judging, after the earlier
	// zero-match returns - its empty answer names itself the same way.
	if in.State != "" && len(out.Summary) == 0 && len(out.Findings) == 0 {
		out.Note = fmt.Sprintf("state=%s matched none of the %d finding(s) the other filters kept; drop it to list them", in.State, len(matched))
	}
	var omittedSummary, omittedFindings int
	out.Summary, omittedSummary = capRows(out.Summary)
	out.Findings, omittedFindings = capRows(out.Findings)
	out.Omitted = omittedSummary + omittedFindings
	return nil, out, nil
}

type explainIn struct {
	selectionIn
	Symbol   string `json:"symbol,omitempty" jsonschema:"the mutated symbol to explain; empty explains the whole document's promotion state"`
	Label    string `json:"label,omitempty" jsonschema:"with no symbol, restrict the promotion triage to findings carrying this label"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type explainedSurvivor struct {
	Position  string `json:"position"`
	Operator  string `json:"operator"`
	Execution string `json:"execution,omitempty"`
	Advice    string `json:"advice" jsonschema:"the action the execution bucket prescribes"`
}

type promotionGroup struct {
	Reason         string   `json:"reason"`
	Count          int      `json:"count"`
	Symbols        []string `json:"symbols"`
	OmittedSymbols int      `json:"omittedSymbols,omitempty"`
}

type explainOut struct {
	Symbol              string                `json:"symbol,omitempty"`
	State               gomutant.FindingState `json:"state,omitempty"`
	Reason              string                `json:"reason,omitempty"`
	Layer               string                `json:"layer,omitempty"`
	LayerReasons        []string              `json:"layerReasons,omitempty" jsonschema:"every portable-line clause the record fails, not only the first"`
	OmittedLayerReasons int                   `json:"omittedLayerReasons,omitempty"`
	Open                []explainedSurvivor   `json:"open,omitempty"`
	OmittedOpen         int                   `json:"omittedOpen,omitempty"`
	Attested            int                   `json:"attested,omitempty"`
	RepoCommittable     *int                  `json:"repoCommittable,omitempty" jsonschema:"document arm: records portable enough for the committed findings document"`
	Document            string                `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	LocalOnly           *int                  `json:"localOnly,omitempty" jsonschema:"document arm: records held in the machine-local overlay"`
	Promotion           []promotionGroup      `json:"promotion,omitempty" jsonschema:"document arm: machine-local records grouped by failing portable-line clause"`
	Note                string                `json:"note,omitempty" jsonschema:"set when the document arm has nothing to group: says whether the document is empty or the label matched nothing, and the next step"`
	OmittedGroups       int                   `json:"omittedGroups,omitempty"`
}

// toolExplain answers why: a symbol's record explained causally (state,
// full machine-local clause list, per-survivor prescriptions), or the
// document's promotion triage. Projection only - no tests run, and the
// advisory stance holds: causes and prescriptions, never a verdict.
func (s *Server) toolExplain(ctx context.Context, req *mcp.CallToolRequest, in explainIn) (*mcp.CallToolResult, explainOut, error) {
	const groupCap, groupSymbolCap, openCap, reasonCap = 50, 10, 20, 20
	if in.Symbol != "" && in.Label != "" {
		return nil, explainOut{}, fmt.Errorf("explain: the label filter restricts the triage arm; pass symbol or label, not both")
	}
	store, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		return nil, explainOut{}, err
	}
	all, err := store.Load(ctx)
	if err != nil {
		return nil, explainOut{}, err
	}
	if in.Symbol != "" {
		for _, finding := range all {
			if err := ctx.Err(); err != nil {
				return nil, explainOut{}, err
			}
			if finding.Symbol != in.Symbol {
				continue
			}
			notify := progressNotifier(ctx, req)
			tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
			if err != nil {
				return nil, explainOut{}, err
			}
			// Freshness judging can be minutes-class; the stretch
			// announces itself and rides the heartbeat so it never
			// reads as a hang (REQ-mcp-envelope).
			if notify != nil {
				notify("inspecting " + finding.Symbol)
			}
			inspection, err := withHeartbeat(ctx, notify, "inspecting "+finding.Symbol, func(ctx context.Context) (gomutant.FindingInspection, error) {
				return tree.InspectFindingContext(ctx, finding)
			})
			if err != nil {
				return nil, explainOut{}, err
			}
			layer, layerReasons := store.LayerReasons(finding)
			out := explainOut{
				Symbol: finding.Symbol, State: inspection.State, Reason: inspection.Reason,
				Layer: layer, LayerReasons: gomutant.RollUpMachineLocalInputs(layerReasons),
				Attested: len(finding.AttestedDispositions()),
				Document: s.findingsPath(in.Findings),
			}
			if len(out.LayerReasons) > reasonCap {
				out.OmittedLayerReasons = len(out.LayerReasons) - reasonCap
				out.LayerReasons = out.LayerReasons[:reasonCap]
			}
			open := finding.Open()
			for i, survivor := range open {
				if i == openCap {
					out.OmittedOpen = len(open) - openCap
					break
				}
				out.Open = append(out.Open, explainedSurvivor{
					Position: survivor.Position, Operator: survivor.Operator,
					Execution: survivor.Execution, Advice: gomutant.SurvivorAdvice(survivor.Execution),
				})
			}
			return nil, out, nil
		}
		return nil, explainOut{}, fmt.Errorf("explain: no finding for %s; the findings tool lists the recorded symbols", in.Symbol)
	}
	repo, local := 0, 0
	groups := map[string][]string{}
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return nil, explainOut{}, err
		}
		if in.Label != "" && !containsLabel(finding.Labels, in.Label) {
			continue
		}
		layer, layerReasons := store.LayerReasons(finding)
		if layer == "repo" {
			repo++
			continue
		}
		local++
		for _, reason := range gomutant.RollUpMachineLocalInputs(layerReasons) {
			groups[reason] = append(groups[reason], finding.Symbol)
		}
	}
	out := explainOut{RepoCommittable: &repo, LocalOnly: &local, Document: s.findingsPath(in.Findings)}
	// An empty triage is an answer with two different next steps -
	// measure first, or widen the label - so the response says which
	// (REQ-mcp-explain, REQ-mcp-envelope).
	switch {
	case len(all) == 0:
		out.Note = "no findings recorded at " + out.Document + " - run measures the tree first"
	case in.Label != "" && repo+local == 0:
		out.Note = fmt.Sprintf("label %q matched none of %d recorded finding(s); drop it to triage the document", in.Label, len(all))
	}
	reasons := make([]string, 0, len(groups))
	for reason := range groups {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if len(groups[reasons[i]]) != len(groups[reasons[j]]) {
			return len(groups[reasons[i]]) > len(groups[reasons[j]])
		}
		return reasons[i] < reasons[j]
	})
	for i, reason := range reasons {
		if i == groupCap {
			out.OmittedGroups = len(reasons) - groupCap
			break
		}
		symbols := groups[reason]
		sort.Strings(symbols)
		group := promotionGroup{Reason: reason, Count: len(symbols)}
		if len(symbols) > groupSymbolCap {
			group.Symbols = symbols[:groupSymbolCap]
			group.OmittedSymbols = len(symbols) - groupSymbolCap
		} else {
			group.Symbols = symbols
		}
		out.Promotion = append(out.Promotion, group)
	}
	return nil, out, nil
}

func containsLabel(labels []string, want string) bool {
	for _, label := range labels {
		if label == want {
			return true
		}
	}
	return false
}

type attestIn struct {
	selectionIn
	Symbol   string `json:"symbol" jsonschema:"the mutated symbol"`
	Position string `json:"position" jsonschema:"the survivor's position (file.go:line:col), as reported"`
	Operator string `json:"operator" jsonschema:"the survivor's operator, as reported"`
	Reason   string `json:"reason" jsonschema:"why the mutant is equivalent"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

// attestedEcho restates a recorded disposition in structured fields —
// operators contain spaces, so a joined string would be unparseable.
type attestedEcho struct {
	Symbol   string `json:"symbol"`
	Position string `json:"position"`
	Operator string `json:"operator"`
}

type attestOut struct {
	Recorded    *attestedEcho `json:"recorded,omitempty" jsonschema:"the disposition as recorded, echoed so the write is confirmed, not inferred"`
	Open        int           `json:"open" jsonschema:"the symbol's open findings after the disposition"`
	Layer       string        `json:"layer" jsonschema:"repo when the record is committable, local when it stays in the machine-local overlay"`
	LayerReason string        `json:"layerReason,omitempty" jsonschema:"why a local record is not portable repo evidence"`
	Warning     string        `json:"warning,omitempty" jsonschema:"set when the record cannot serve as it stands - the next measure judges the equivalence afresh and sheds the disposition if its mutation domain moved"`
}

func (s *Server) toolAttest(ctx context.Context, req *mcp.CallToolRequest, in attestIn) (*mcp.CallToolResult, attestOut, error) {
	var out attestOut
	for _, f := range map[string]string{"symbol": in.Symbol, "position": in.Position, "operator": in.Operator, "reason": in.Reason} {
		if f == "" {
			return nil, out, fmt.Errorf("attest_survivor needs symbol, position, operator, and reason")
		}
	}
	var attested gomutant.Finding
	err := s.update(ctx, s.findingsPath(in.Findings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == in.Symbol {
				if err := all[i].Attest(in.Position, in.Operator, in.Reason); err != nil {
					return nil, err
				}
				out.Open = len(all[i].Open())
				attested = all[i]
				return all, nil
			}
		}
		return nil, fmt.Errorf("no finding for %s", in.Symbol)
	})
	if err != nil {
		return nil, out, err
	}
	out.Recorded = &attestedEcho{Symbol: in.Symbol, Position: in.Position, Operator: in.Operator}
	// The echo states where the record lives and whether it can serve
	// as it stands: a disposition on a record whose pins moved is
	// judged afresh - and shed if rejected - by the next measure
	// (REQ-attest-survivor). The disposition is already recorded, so
	// echo failures demote to warnings - a hard error here would read
	// as a failed write that in fact landed.
	store, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		out.Warning = "record state unavailable: " + err.Error()
		return nil, out, nil
	}
	out.Layer, out.LayerReason = store.Layer(attested)
	notify := progressNotifier(ctx, req)
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		out.Warning = "record state unavailable: " + err.Error()
		return nil, out, nil
	}
	inspection, err := tree.InspectFindingContext(ctx, attested)
	if err != nil {
		out.Warning = "record state unavailable: " + err.Error()
		return nil, out, nil
	}
	if inspection.State != gomutant.FindingCurrent {
		out.Warning = fmt.Sprintf("the record is %s (%s) - the disposition is judged afresh when %s is re-measured", inspection.State, inspection.Reason, in.Symbol)
	}
	return nil, out, nil
}

type pruneIn struct {
	selectionIn
	Check    bool   `json:"check,omitempty" jsonschema:"preview the removals without touching the document"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type prunedOut struct {
	Symbol   string                 `json:"symbol"`
	Attested []gomutant.Attestation `json:"attested,omitempty" jsonschema:"the removed record's dispositions, echoed so the reasoning survives the removal"`
}

type pruneOut struct {
	Removed  []prunedOut `json:"removed" jsonschema:"never truncated - for overlay-resident records this echo is the disposition reasoning's last home"`
	Kept     int         `json:"kept"`
	Check    bool        `json:"check,omitempty"`
	Document string      `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
}

func (s *Server) toolPrune(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, pruneOut, error) {
	out := pruneOut{Removed: []prunedOut{}, Document: s.findingsPath(in.Findings)}
	store, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		return nil, out, err
	}
	result, err := tree.PruneDetachedContext(ctx, store, in.Check)
	if err != nil {
		return nil, out, err
	}
	out.Kept, out.Check = result.Kept, result.Check
	// The removal echo is never truncated: for an overlay-resident
	// record the response is the disposition reasoning's last home, and
	// a capped check preview would hide part of what a destructive call
	// is about to delete (REQ-mcp-lifecycle's envelope exception).
	for _, record := range result.Removed {
		out.Removed = append(out.Removed, prunedOut{Symbol: record.Symbol, Attested: record.Attested})
	}
	return nil, out, nil
}

type retargetIn struct {
	selectionIn
	From     string `json:"from" jsonschema:"old symbol prefix: a package pair renames a package (a dot-terminated pass covers its own symbols, a slash-terminated pass its subpackages); a symbol pair renames within its package, segment for segment"`
	To       string `json:"to" jsonschema:"new symbol prefix, terminated like from"`
	Check    bool   `json:"check,omitempty" jsonschema:"preview the rewrites without touching the document"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type retargetOut struct {
	Rewritten        []gomutant.RetargetedRecord `json:"rewritten" jsonschema:"records whose mutated symbol changed"`
	Touched          int                         `json:"touched,omitempty" jsonschema:"records the rename's closure updated without renaming their own symbol (an oracle or killer in the renamed surface)"`
	TouchedRewrites  []gomutant.TouchedRewrite   `json:"touchedRewrites,omitempty" jsonschema:"the touched records' field rewrites - the surface no resolution gate reaches, echoed for audit"`
	OmittedRewritten int                         `json:"omittedRewritten,omitempty" jsonschema:"rewritten rows beyond the response cap - counted, not listed; under check they are previews"`
	OmittedTouched   int                         `json:"omittedTouched,omitempty" jsonschema:"touched rewrite rows beyond the response cap - counted, not listed"`
	Check            bool                        `json:"check,omitempty"`
	Document         string                      `json:"document,omitempty" jsonschema:"the findings document path carrying the full uncapped set"`
	Note             string                      `json:"note,omitempty" jsonschema:"set when the prefix matched nothing: the rename touched no record, and the findings tool lists the recorded symbols"`
}

func (s *Server) toolRetarget(ctx context.Context, req *mcp.CallToolRequest, in retargetIn) (*mcp.CallToolResult, retargetOut, error) {
	out := retargetOut{Rewritten: []gomutant.RetargetedRecord{}, Document: s.findingsPath(in.Findings)}
	store, err := gomutant.OpenStore(s.findingsPath(in.Findings), s.dir)
	if err != nil {
		return nil, out, err
	}
	notify := progressNotifier(ctx, req)
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		return nil, out, err
	}
	result, err := tree.RetargetContext(ctx, store, in.From, in.To, in.Check)
	if err != nil {
		return nil, out, err
	}
	out.Check = result.Check
	out.Touched = result.Touched
	out.Rewritten = append(out.Rewritten, result.Rewritten...)
	out.TouchedRewrites = append(out.TouchedRewrites, result.TouchedRewrites...)
	// A rename that moved nothing is an answer with a next step, not an
	// empty success: the prefix either mismatches the recorded spelling
	// or the rewrite already landed (REQ-mcp-envelope).
	if len(out.Rewritten) == 0 && out.Touched == 0 {
		out.Note = fmt.Sprintf("prefix %q matched no records; the findings tool lists the recorded symbols", in.From)
	}
	out.Rewritten, out.OmittedRewritten = capRows(out.Rewritten)
	out.TouchedRewrites, out.OmittedTouched = capRows(out.TouchedRewrites)
	return nil, out, nil
}

type ephemeralIn struct {
	selectionIn
	File             string               `json:"file,omitempty" jsonschema:"tree-relative source file for replacement or edits; omit for batch_edits"`
	Replacement      string               `json:"replacement,omitempty" jsonschema:"the whole replacement source; give exactly one mutation form"`
	Edits            []gomutant.Edit      `json:"edits,omitempty" jsonschema:"exact-match edits applied sequentially — each old must match exactly once in the content the prior edits produced; state the change, not the file"`
	BatchEdits       []gomutant.BatchEdit `json:"batch_edits,omitempty" jsonschema:"atomic file-scoped exact-match edits; every match resolves against the original file snapshot"`
	TestPkg          string               `json:"test_pkg" jsonschema:"go package path whose named test decides the kill"`
	Run              string               `json:"run" jsonschema:"-run pattern naming the deciding test"`
	TimeoutSec       *int                 `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before attributed result completion after this many seconds; omitted means 300, and an explicit 0 means unlimited"`
	OracleTimeoutSec int                  `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
	OracleMemoryMiB  *int64               `json:"oracle_memory_mib,omitempty" jsonschema:"memory ceiling for the probe's oracle process tree in MiB: absent inherits the server's installed ceiling, 0 derives RAM/2 floored at 1 GiB for this probe, -1 disables for this probe; refused while a run is in flight - the campaign owns the process ceiling"`
	Runs             int                  `json:"runs,omitempty" jsonschema:"run the mutant this many times against the once-probed baseline (1-10, default 1): killed means every run killed - N consecutive kills split a deterministic kill from a property generator's draw luck; per-run verdicts ride the result"`
	Attest           string               `json:"attest,omitempty" jsonschema:"record the surviving probe as a judged equivalence with this reasoning, in the committed record beside the findings document; refused when the probe killed, was mixed, or never exercised the edit"`
	Findings         string               `json:"findings,omitempty" jsonschema:"findings document path whose sibling ephemeral-attestation record attest writes (default .gomutant/findings.json)"`
}

// ephemeralOut is the probe result plus the attestation confirmation
// when attest was requested.
type ephemeralOut struct {
	*gomutant.EphemeralResult
	AttestationRecorded string `json:"attestationRecorded,omitempty" jsonschema:"edit digest of the equivalence attestation appended to the committed record"`
	AttestationPath     string `json:"attestationPath,omitempty" jsonschema:"the committed record the attestation was written to"`
}

func (s *Server) toolEphemeral(ctx context.Context, req *mcp.CallToolRequest, in ephemeralIn) (*mcp.CallToolResult, *ephemeralOut, error) {
	// The ceiling is process state a running campaign owns: an explicit
	// probe ceiling while a run is in flight would diverge the campaign's
	// evidence from its stamped pin (a mutant and its baseline could even
	// straddle the two ceilings), so it refuses loudly instead of racing.
	// Without a run in flight the probe's ceiling installs for the
	// probe's duration and the exact prior state - the installed flag
	// included - restores after.
	// Process-wide overrides (memory ceiling, probe width) install only
	// under the shared width-claim guard: the old check-then-install
	// read runsInFlight atomically but installed outside any lock, so a
	// campaign admitted concurrently could interleave with the probe's
	// deferred restore and run later spawns under a ceiling diverged
	// from its recorded pin. The claim admits the probe only while no
	// campaign is in flight AND blocks campaigns until release, closing
	// the probe-vs-campaign window (REQ-exec-oracle-parallelism,
	// REQ-exec-oracle-memory). Probe-vs-probe interleaving remains: two
	// concurrent probes' restores can interleave, bounded and
	// self-healing (results are never persisted; the next install
	// resets) - tracked in the train plan.
	if err := s.claimProbeOverride(); err != nil {
		if in.OracleMemoryMiB != nil {
			return nil, nil, err
		}
		// No explicit override requested: the probe correctly inherits
		// the in-flight campaign's width and ceiling.
	} else {
		defer s.releaseProbeOverride()
		if in.OracleMemoryMiB != nil {
			prior := gomutant.SnapshotOracleMemory()
			gomutant.SetOracleMemoryLimit(mcpOracleMemoryBytes(in.OracleMemoryMiB), 1)
			defer gomutant.RestoreOracleMemory(prior)
		}
		// A prior run's inner-parallelism cap must not throttle a lone
		// probe in this long-lived process: between campaigns the probe
		// is the only oracle tree, so it runs at jobs=1 width (full),
		// the prior state restored after (REQ-exec-oracle-parallelism).
		prior := gomutant.SnapshotOracleParallelism()
		gomutant.SetOracleParallelism(1)
		defer gomutant.RestoreOracleParallelism(prior)
	}
	timeout, err := commandTimeout("timeout_sec", in.TimeoutSec)
	if err != nil {
		return nil, nil, err
	}
	oracleTimeout, err := secondsDuration("oracle_timeout_sec", in.OracleTimeoutSec)
	if err != nil {
		return nil, nil, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if in.TestPkg == "" || in.Run == "" {
		return nil, nil, fmt.Errorf("ephemeral needs test_pkg and run")
	}
	if in.Runs < 0 || in.Runs > gomutant.MaxEphemeralRuns {
		return nil, nil, fmt.Errorf("runs %d is outside 1-%d (omitted means 1)", in.Runs, gomutant.MaxEphemeralRuns)
	}
	forms := 0
	if in.Replacement != "" {
		forms++
	}
	if len(in.Edits) != 0 {
		forms++
	}
	if len(in.BatchEdits) != 0 {
		forms++
	}
	if forms != 1 {
		return nil, nil, fmt.Errorf("give replacement, edits, or batch_edits, exactly one")
	}
	if len(in.BatchEdits) == 0 {
		if in.File == "" {
			return nil, nil, fmt.Errorf("replacement and edits need file")
		}
		if err := localPath("file", in.File); err != nil {
			return nil, nil, err
		}
	} else {
		if in.File != "" {
			return nil, nil, fmt.Errorf("batch_edits carries its own files; omit file")
		}
		for i, edit := range in.BatchEdits {
			if err := localPath(fmt.Sprintf("batch_edits[%d].file", i), edit.File); err != nil {
				return nil, nil, err
			}
		}
	}
	// The ephemeral library path exposes no per-step callbacks, so progress
	// is limited to the two coarse boundaries the tool itself crosses.
	notify := progressNotifier(ctx, req)
	if notify != nil {
		notify("prepare loading")
	}
	tree, err := withHeartbeat(ctx, notify, "loading tree", func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, in.selection()) })
	if err != nil {
		return nil, nil, err
	}
	if notify != nil {
		notify("running " + in.TestPkg)
	}
	res, err := withHeartbeat(ctx, notify, "ephemeral oracle", func(ctx context.Context) (*gomutant.EphemeralResult, error) {
		switch {
		case len(in.BatchEdits) > 0:
			return tree.EphemeralBatch(ctx, in.BatchEdits, in.TestPkg, in.Run, oracleTimeout, in.Runs)
		case len(in.Edits) > 0:
			return tree.EphemeralEdits(ctx, in.File, in.Edits, in.TestPkg, in.Run, oracleTimeout, in.Runs)
		default:
			return tree.Ephemeral(ctx, in.File, []byte(in.Replacement), in.TestPkg, in.Run, oracleTimeout, in.Runs)
		}
	})
	if err != nil {
		return nil, nil, err
	}
	out := &ephemeralOut{EphemeralResult: res}
	if in.Attest != "" {
		att, err := gomutant.AttestEphemeralEquivalence(ctx, s.dir, res, in.Attest)
		if err != nil {
			return nil, nil, err
		}
		path := gomutant.EphemeralAttestationsPathFor(s.findingsPath(in.Findings))
		if err := gomutant.RecordEphemeralAttestation(ctx, path, att); err != nil {
			return nil, nil, err
		}
		out.AttestationRecorded = att.EditDigest
		out.AttestationPath = path
	}
	return nil, out, nil
}

// mcpOracleMemoryBytes converts the optional MiB input: absent or 0
// derives the default, negative disables.
func mcpOracleMemoryBytes(mib *int64) int64 {
	if mib == nil || *mib == 0 {
		return 0
	}
	if *mib < 0 {
		return -1
	}
	return *mib << 20
}

// guidanceDoc is the embedded guidance document; a malformed document
// is a build defect the parse-pinning test surfaces, so consumers
// fail loudly rather than serving nothing.
func guidanceDoc() *guidancepkg.Document {
	doc, err := gomutant.GuidanceDocument()
	if err != nil {
		panic("mcpserver: embedded guidance document malformed: " + err.Error())
	}
	return doc
}

func guidanceOrientation() string { return guidanceDoc().Orientation() }

// guidanceDescription is a tool's one-line purpose, served from the
// guidance document under the tool's mcp spelling
// (REQ-mcp-guidance).
func guidanceDescription(verb string) string {
	d, err := guidanceDoc().Description("mcp", verb)
	if err != nil {
		panic("mcpserver: " + err.Error())
	}
	return d
}

// guidanceIn asks for one verb's section or, empty, the decision map.
type guidanceIn struct {
	Verb string `json:"verb,omitempty" jsonschema:"the verb to describe; empty serves the decision map"`
}

// toolGuidance serves the embedded guidance document
// (REQ-mcp-guidance): a verb's full section under its mcp spelling,
// or the decision map for orientation. It touches no tree state.
func (s *Server) toolGuidance(ctx context.Context, req *mcp.CallToolRequest, in guidanceIn) (*mcp.CallToolResult, any, error) {
	if in.Verb == "" {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: guidanceDoc().Orientation()}}}, nil, nil
	}
	long, err := guidanceDoc().Long("mcp", in.Verb)
	if err != nil {
		return nil, nil, fmt.Errorf("%w; empty verb serves the decision map, which names every verb", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: long}}}, nil, nil
}
