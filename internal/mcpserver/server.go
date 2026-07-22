// Package mcpserver serves gomutant over the Model Context Protocol: the
// library's operations as tools, a thin shell exactly like the CLI so the two
// faces cannot drift (spec mcp.md). It inherits the advisory stance whole —
// no tool renders a pass/fail verdict (REQ-result-findings).
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
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
}

// New builds a server rooted at dir.
func New(dir string) *Server {
	return &Server{dir: dir}
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
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gomutant", Version: "v0"}, &mcp.ServerOptions{
		Instructions: "gomutant measures whether tests notice mutations. The loop: run measures targets (whole tree, changed vs a git ref, or a targets document) and maintains the findings document incrementally - prior findings with matching pins are served, and each decision line says why; findings inspects the document (state, cause, survivors with execution buckets, candidate evidence, repo/local layer) without running anything; attest_survivor dispositions an equivalent mutant with the reasoning on record; ephemeral probes one hand-written mutant without persisting; discover lists effective targets without measuring. Survivors are findings awaiting disposition - strengthen a test or attest an equivalence - never verdicts. A survivor bucketed never-executed wants coverage; executed-and-passed wants a sharper assertion or an attestation. Send a progress token on run/ephemeral for phase notifications and a heartbeat; long campaigns exceed MCP client timeouts - raise timeout_sec or use the CLI. Responses cap long lists and count the remainder; the findings document on disk is always complete.",
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run",
		Description: "Mutate the targets and run each one's oracle tests per mutant. Targets come from a document (gomutant's or stipulator's export), changed-scope discovery vs a git ref, or whole-tree discovery. Maintains the findings document: prior findings with matching pins are served, the rest re-measure, and each finished target commits incrementally so an interrupted run keeps completed targets. Survivors are findings awaiting disposition, never verdicts. Each mutant's oracle executes once, bracketing runtime-input observation. With a progress token: phase notifications plus a heartbeat. Preparation and decision streams leave the response when streamed; long lists cap with the remainder counted. timeout_sec defaults to 300 seconds when omitted (an explicit 0 means unlimited); use the CLI for work that may exceed the MCP client's request timeout.",
	}, s.toolRun)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "discover",
		Description: "List effective target symbols, sorted opaque labels, explicit or package-derived oracle mode, skip reasons, and changed-scope residue. Counts lead the response; target rows cap at 50 unless detail=true. Exact oracles are deduplicated in top-level oracleSets; each target's oracleSet integer references oracleSets[].id.",
	}, s.toolDiscover)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "findings",
		Description: "Inspect every findings record as current, stale, unverifiable, or detached, with its open survivors, attested dispositions, and per-candidate unverifiable runtime evidence (candidateEvidence). Each record states its persistence layer: repo (portable, in the committed findings document) or local (machine-local overlay, with the reason it is not committable). Filter by opaque label; inspection runs no tests.",
	}, s.toolFindings)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_survivor",
		Description: "Disposition a surviving mutant as equivalent, with the reasoning on record. Refused unless the mutant is among the finding's current survivors; shed automatically when any pin moves, so every body version is re-judged.",
	}, s.toolAttest)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ephemeral",
		Description: "Run one manual mutant the operator set cannot generate: replace one file whole, apply sequential edits to one file, or apply an atomic exact-match edit batch across files, then check whether the named test kills it. The tree is never touched; the result is evidence, never persisted. An observed probe executes the named test once, bracketing runtime-input observation. timeout_sec defaults to 300 seconds when omitted (an explicit 0 means unlimited).",
	}, s.toolEphemeral)
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
// drill-down via the findings tool, not run payload.
func capRunFindings(findings []gomutant.Finding) (rows []findingOut, omitted int) {
	const findingRowCap, openCap = 50, 20
	for _, f := range findings {
		if len(rows) == findingRowCap {
			omitted++
			continue
		}
		open := f.Open()
		omittedOpen := 0
		if len(open) > openCap {
			omittedOpen = len(open) - openCap
			open = open[:openCap]
		}
		rows = append(rows, findingOut{
			Symbol: f.Symbol, Labels: f.Labels,
			CandidateCount: f.CandidateCount, Generated: f.Generated,
			Mutants: f.Mutants, Killed: f.Killed, Discarded: f.Discarded,
			Attested: len(f.Attested), Operators: append([]gomutant.OperatorSummary{}, f.Operators...), Open: open,
			OmittedOpen: omittedOpen,
			Cached:      f.Cached, Skipped: f.Skipped,
		})
	}
	return rows, omitted
}

// guidanceOut is one oracle set's instability attribution shared by the
// targets it covers: the chunk-level memo already computes one
// attribution per set, so the response aggregates instead of repeating
// a near-identical suggestion per target (REQ-mcp-envelope).
type guidanceOut struct {
	Targets       []string `json:"targets" jsonschema:"targets whose unverifiable evidence this attribution covers"`
	UnstableTests []string `json:"unstableTests,omitempty"`
	Reason        string   `json:"reason,omitempty" jsonschema:"the first covered finding's unverifiable reason"`
	Suggestion    string   `json:"suggestion"`
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

type runIn struct {
	TargetsPath      string   `json:"targets_path,omitempty" jsonschema:"path to a targets document (gomutant's format or stipulator's export); overrides discovery"`
	TargetsJSON      string   `json:"targets_json,omitempty" jsonschema:"an inline targets document, same formats as targets_path"`
	Changed          string   `json:"changed,omitempty" jsonschema:"target only symbols whose bodies differ from this git ref (requires git)"`
	Budget           int      `json:"budget,omitempty" jsonschema:"candidates per symbol; 0 means exhaustive"`
	TimeoutSec       *int     `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before the final findings commit after this many seconds; omitted means 300, and an explicit 0 means unlimited"`
	OracleTimeoutSec int      `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
	Jobs             int      `json:"jobs,omitempty" jsonschema:"concurrent mutant runs; 0 means half the CPUs"`
	BracketPaths     []string `json:"bracket_paths,omitempty" jsonschema:"external surfaces the oracle legitimately reads (module-relative paths or absolute files; absolute directories and tool-excluded paths are refused); extends each spawn's observation bracket, carrying the caller's assertion the surface is mutation-free for the run"`
	Force            bool     `json:"force,omitempty" jsonschema:"re-measure even targets whose prior finding still covers the request; the pin spans the mutated symbol's body, every oracle test's source closure, and the observed runtime inputs (toolchain, build configuration, and the other measurement pins are always compared too), so new or changed oracle tests re-measure without force"`
	Findings         string   `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json), read and updated"`
	Packages         []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols          []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
}

type findingOut struct {
	Symbol         string                       `json:"symbol"`
	Labels         []string                     `json:"labels,omitempty"`
	CandidateCount int                          `json:"candidateCount"`
	Generated      int                          `json:"generated"`
	Mutants        int                          `json:"mutants"`
	Killed         int                          `json:"killed"`
	Discarded      int                          `json:"discarded"`
	Operators      []gomutant.OperatorSummary   `json:"operators"`
	Attested       int                          `json:"attested,omitempty"`
	Open           []gomutant.Survivor          `json:"open,omitempty"`
	OmittedOpen    int                          `json:"omittedOpen,omitempty" jsonschema:"open survivors beyond the response cap; the findings tool serves the full set"`
	Cached         bool                         `json:"cached,omitempty"`
	Skipped        string                       `json:"skipped,omitempty"`
}

type runOut struct {
	Summary          gomutant.RunSummary         `json:"summary"`
	Document         string                      `json:"document"`
	Findings         []findingOut                `json:"findings"`
	OmittedFindings  int                         `json:"omittedFindings,omitempty" jsonschema:"finding rows beyond the response cap; the document carries the full set"`
	Guidance         []guidanceOut               `json:"oracleGuidance,omitempty" jsonschema:"oracle-instability attributions aggregated per oracle set: targets sharing one unstable oracle share one entry"`
	Residue          []gomutant.Residue          `json:"residue,omitempty"`
	OmittedResidue   int                         `json:"omittedResidue,omitempty"`
	Preparation      []gomutant.PreparationEvent `json:"preparation,omitempty" jsonschema:"absent when a progress token streamed the events; preparationCount still totals them"`
	PreparationCount int                         `json:"preparationCount"`
	Decisions        []gomutant.RunDecision      `json:"decisions,omitempty" jsonschema:"absent when a progress token streamed the decisions; decisionsCount still totals them"`
	DecisionsCount   int                         `json:"decisionsCount"`
}

func (s *Server) toolRun(ctx context.Context, req *mcp.CallToolRequest, in runIn) (result *mcp.CallToolResult, out runOut, err error) {
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
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
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
	tree, err := s.loadTreeContext(ctx)
	if err != nil {
		return nil, out, err
	}
	var targets []gomutant.Target
	wholeTree := false
	forms := 0
	if in.TargetsPath != "" {
		forms++
	}
	if in.TargetsJSON != "" {
		forms++
	}
	if in.Changed != "" {
		forms++
	}
	if forms > 1 {
		return nil, out, fmt.Errorf("give targets_path, targets_json, or changed, at most one")
	}
	switch {
	case in.TargetsPath != "":
		if err := localPath("targets_path", in.TargetsPath); err != nil {
			return nil, out, err
		}
		data, err := contextio.ReadFile(ctx, filepath.Join(s.dir, filepath.FromSlash(in.TargetsPath)))
		if err != nil {
			return nil, out, err
		}
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		if targets, err = gomutant.LoadTargetsContext(ctx, data); err != nil {
			return nil, out, err
		}
	case in.TargetsJSON != "":
		if targets, err = gomutant.LoadTargetsContext(ctx, []byte(in.TargetsJSON)); err != nil {
			return nil, out, err
		}
	case in.Changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, s.dir, in.Changed)
		if err != nil {
			return nil, out, err
		}
		targets, out.Residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, s.dir, in.Changed, p)
		})
		if err != nil {
			return nil, out, err
		}
	default:
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return nil, out, err
		}
		wholeTree = true
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
	if len(in.Packages) != 0 || len(in.Symbols) != 0 {
		wholeTree = false
	}
	if len(targets) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		out.Document = s.findingsPath(in.Findings)
		if wholeTree {
			err := s.update(ctx, out.Document, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return gomutant.MergeWholeFindings(current, nil, nil), nil
			})
			if err != nil {
				return nil, out, err
			}
		}
		return nil, out, nil
	}
	prior, err := s.loadFindingsContext(ctx, in.Findings)
	if err != nil {
		return nil, out, err
	}
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	streams := newRunStreams(&out, notify)
	options := gomutant.Options{
		Budget:        in.Budget,
		OracleTimeout: oracleTimeout,
		Jobs:          in.Jobs,
		Force:         in.Force,
		BracketPaths:  in.BracketPaths,
		Guidance:      func(g gomutant.OracleGuidance) { appendGuidance(&out.Guidance, g) },
		Prior:         prior,
		Decision:      streams.decision,
		Progress:      streams.progress,
		// Each finished target commits under the same document lock the final
		// merge takes, so an interrupted run keeps its completed targets; the
		// final merge below remains the authority (REQ-exec-cancellation).
		Commit: func(finding gomutant.Finding) error {
			return s.update(ctx, s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return gomutant.MergeFindings(current, []gomutant.Finding{finding}), nil
			})
		},
	}
	if notify != nil {
		options.AnalysisProgress = func(phase, pkg string) {
			message := "analysis " + phase
			if pkg != "" {
				message += " " + pkg
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
		return nil, out, err
	}
	out.Summary = gomutant.SummarizeRun(findings)
	if err := ctx.Err(); err != nil {
		return nil, out, err
	}
	out.Findings, out.OmittedFindings = capRunFindings(findings)
	const residueCap = 50
	if len(out.Residue) > residueCap {
		out.OmittedResidue = len(out.Residue) - residueCap
		out.Residue = out.Residue[:residueCap]
	}
	err = s.update(ctx, s.findingsPath(in.Findings), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wholeTree {
			return gomutant.MergeWholeFindings(current, findings, targets), nil
		}
		return gomutant.MergeFindings(current, findings), nil
	})
	if err != nil {
		return nil, out, err
	}
	out.Document = s.findingsPath(in.Findings)
	// A drift-refused campaign persists its completed findings and still
	// errors: the client never reads a partial campaign as success
	// (REQ-exec-quiescence).
	if drift != nil {
		return nil, out, drift
	}
	return nil, out, nil
}

type discoverIn struct {
	TargetsPath string   `json:"targets_path,omitempty" jsonschema:"path to a targets document; overrides discovery"`
	TargetsJSON string   `json:"targets_json,omitempty" jsonschema:"inline targets document; overrides discovery"`
	Changed     string   `json:"changed,omitempty" jsonschema:"changed-scope vs this git ref; empty means the whole tree"`
	Packages    []string `json:"packages,omitempty" jsonschema:"complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives"`
	Symbols     []string `json:"symbols,omitempty" jsonschema:"complete fully qualified symbol glob filters; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*; alternatives"`
	Detail      bool     `json:"detail,omitempty" jsonschema:"return every target and residue row; default caps rows at 50 with the remainder counted"`
}

type discoverTarget struct {
	Symbol         string   `json:"symbol" jsonschema:"fully qualified target symbol"`
	OracleSet      int      `json:"oracleSet" jsonschema:"zero-based id that references one entry in the top-level oracleSets array"`
	Labels         []string `json:"labels,omitempty" jsonschema:"sorted opaque labels carried unchanged from the target"`
	OracleExplicit bool     `json:"oracleExplicit" jsonschema:"whether the referenced oracle was explicitly supplied rather than package-derived"`
	Skipped        string   `json:"skipped,omitempty" jsonschema:"reason this target cannot be measured, when applicable"`
}

type discoverOracleSet struct {
	ID     int      `json:"id" jsonschema:"zero-based oracle-set id referenced by targets[].oracleSet"`
	Oracle []string `json:"oracle" jsonschema:"sorted fully qualified test symbols forming this exact effective oracle"`
}

type discoverOut struct {
	TargetCount    int                 `json:"targetCount" jsonschema:"effective targets after filtering; leads the response so a campaign's scale reads before any row"`
	SkippedCount   int                 `json:"skippedCount,omitempty" jsonschema:"targets carrying a skip reason"`
	ResidueCount   int                 `json:"residueCount,omitempty" jsonschema:"changed-but-untargeted paths"`
	OracleSets     []discoverOracleSet `json:"oracleSets" jsonschema:"canonical exact oracle sets assigned in first-target order"`
	Targets        []discoverTarget    `json:"targets" jsonschema:"ordered effective targets whose oracleSet references oracleSets[].id; capped at 50 unless detail=true"`
	OmittedTargets int                 `json:"omittedTargets,omitempty" jsonschema:"target rows beyond the cap; set detail=true for the full set"`
	Residue        []gomutant.Residue  `json:"residue,omitempty"`
	OmittedResidue int                 `json:"omittedResidue,omitempty"`
}

func (s *Server) toolDiscover(ctx context.Context, req *mcp.CallToolRequest, in discoverIn) (*mcp.CallToolResult, discoverOut, error) {
	var out discoverOut
	tree, err := s.loadTreeContext(ctx)
	if err != nil {
		return nil, out, err
	}
	forms := 0
	if in.TargetsPath != "" {
		forms++
	}
	if in.TargetsJSON != "" {
		forms++
	}
	if in.Changed != "" {
		forms++
	}
	if forms > 1 {
		return nil, out, fmt.Errorf("give targets_path, targets_json, or changed, at most one")
	}
	var targets []gomutant.Target
	switch {
	case in.TargetsPath != "":
		if err := localPath("targets_path", in.TargetsPath); err != nil {
			return nil, out, err
		}
		data, err := contextio.ReadFile(ctx, filepath.Join(s.dir, filepath.FromSlash(in.TargetsPath)))
		if err != nil {
			return nil, out, err
		}
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		targets, err = gomutant.LoadTargetsContext(ctx, data)
		if err != nil {
			return nil, out, err
		}
	case in.TargetsJSON != "":
		targets, err = gomutant.LoadTargetsContext(ctx, []byte(in.TargetsJSON))
		if err != nil {
			return nil, out, err
		}
	case in.Changed != "":
		paths, err := gitref.ChangedPathsContext(ctx, s.dir, in.Changed)
		if err != nil {
			return nil, out, err
		}
		targets, out.Residue, err = tree.DiscoverChangedContext(ctx, paths, func(p string) ([]byte, bool) {
			return gitref.ShowContext(ctx, s.dir, in.Changed, p)
		})
		if err != nil {
			return nil, out, err
		}
	default:
		targets, err = tree.DiscoverContext(ctx)
		if err != nil {
			return nil, out, err
		}
	}
	targets, err = tree.FilterTargetsContext(ctx, targets, in.Packages, in.Symbols)
	if err != nil {
		return nil, out, err
	}
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
	// Counts lead; rows cap unless the caller asks for detail
	// (REQ-mcp-envelope).
	const discoverRowCap = 50
	if !in.Detail {
		if len(out.Targets) > discoverRowCap {
			out.OmittedTargets = len(out.Targets) - discoverRowCap
			out.Targets = out.Targets[:discoverRowCap]
		}
		if len(out.Residue) > discoverRowCap {
			out.OmittedResidue = len(out.Residue) - discoverRowCap
			out.Residue = out.Residue[:discoverRowCap]
		}
	}
	return nil, out, nil
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
	Label    string `json:"label,omitempty" jsonschema:"show only findings carrying this label"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
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
	Findings        []inspectedFinding `json:"findings"`
	RepoCommittable int                `json:"repoCommittable" jsonschema:"records portable enough for the committed findings document"`
	LocalOnly       int                `json:"localOnly" jsonschema:"records held in the machine-local overlay a reviewer would not inherit"`
}

func (s *Server) toolFindings(ctx context.Context, req *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, findingsOut, error) {
	out := findingsOut{Findings: []inspectedFinding{}}
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
	if len(all) == 0 {
		return nil, out, nil
	}
	tree, err := s.loadTreeContext(ctx)
	if err != nil {
		return nil, out, err
	}
	for _, finding := range all {
		if err := ctx.Err(); err != nil {
			return nil, out, err
		}
		if in.Label != "" && !containsLabel(finding.Labels, in.Label) {
			continue
		}
		inspection, err := tree.InspectFindingContext(ctx, finding)
		if err != nil {
			return nil, out, err
		}
		layer, layerReason := store.Layer(finding)
		if layer == "repo" {
			out.RepoCommittable++
		} else {
			out.LocalOnly++
		}
		labels := append([]string(nil), finding.Labels...)
		sort.Strings(labels)
		out.Findings = append(out.Findings, inspectedFinding{
			Symbol: finding.Symbol, Labels: labels, State: inspection.State, Reason: inspection.Reason,
			Layer: layer, LayerReason: layerReason,
			CandidateCount: finding.CandidateCount, Generated: finding.Generated,
			Mutants: finding.Mutants, Killed: finding.Killed, Discarded: finding.Discarded,
			Operators: append([]gomutant.OperatorSummary{}, finding.Operators...),
			Open:      append([]gomutant.Survivor{}, finding.Open()...), Attested: append([]gomutant.Attestation{}, finding.AttestedDispositions()...),
			Candidates: inspection.CandidateEvidence,
		})
	}
	sort.Slice(out.Findings, func(i, j int) bool { return out.Findings[i].Symbol < out.Findings[j].Symbol })
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
	Symbol   string `json:"symbol" jsonschema:"the mutated symbol"`
	Position string `json:"position" jsonschema:"the survivor's position (file.go:line:col), as reported"`
	Operator string `json:"operator" jsonschema:"the survivor's operator, as reported"`
	Reason   string `json:"reason" jsonschema:"why the mutant is equivalent"`
	Findings string `json:"findings,omitempty" jsonschema:"findings document path (default .gomutant/findings.json)"`
}

type attestOut struct {
	Open int `json:"open" jsonschema:"the symbol's open findings after the disposition"`
}

func (s *Server) toolAttest(ctx context.Context, req *mcp.CallToolRequest, in attestIn) (*mcp.CallToolResult, attestOut, error) {
	var out attestOut
	for _, f := range map[string]string{"symbol": in.Symbol, "position": in.Position, "operator": in.Operator, "reason": in.Reason} {
		if f == "" {
			return nil, out, fmt.Errorf("attest_survivor needs symbol, position, operator, and reason")
		}
	}
	err := s.update(ctx, s.findingsPath(in.Findings), func(all []gomutant.Finding) ([]gomutant.Finding, error) {
		for i := range all {
			if all[i].Symbol == in.Symbol {
				if err := all[i].Attest(in.Position, in.Operator, in.Reason); err != nil {
					return nil, err
				}
				out.Open = len(all[i].Open())
				return all, nil
			}
		}
		return nil, fmt.Errorf("no finding for %s", in.Symbol)
	})
	return nil, out, err
}

type ephemeralIn struct {
	File             string               `json:"file,omitempty" jsonschema:"tree-relative source file for replacement or edits; omit for batch_edits"`
	Replacement      string               `json:"replacement,omitempty" jsonschema:"the whole replacement source; give exactly one mutation form"`
	Edits            []gomutant.Edit      `json:"edits,omitempty" jsonschema:"exact-match edits applied sequentially — each old must match exactly once in the content the prior edits produced; state the change, not the file"`
	BatchEdits       []gomutant.BatchEdit `json:"batch_edits,omitempty" jsonschema:"atomic file-scoped exact-match edits; every match resolves against the original file snapshot"`
	TestPkg          string               `json:"test_pkg" jsonschema:"go package path whose named test decides the kill"`
	Run              string               `json:"run" jsonschema:"-run pattern naming the deciding test"`
	TimeoutSec       *int                 `json:"timeout_sec,omitempty" jsonschema:"cancel tool work before attributed result completion after this many seconds; omitted means 300, and an explicit 0 means unlimited"`
	OracleTimeoutSec int                  `json:"oracle_timeout_sec,omitempty" jsonschema:"maximum duration of each oracle process in seconds; 0 means 60"`
}

func (s *Server) toolEphemeral(ctx context.Context, req *mcp.CallToolRequest, in ephemeralIn) (*mcp.CallToolResult, *gomutant.EphemeralResult, error) {
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
	tree, err := s.loadTreeContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if notify != nil {
		notify("running " + in.TestPkg)
	}
	if len(in.BatchEdits) > 0 {
		res, err := tree.EphemeralBatch(ctx, in.BatchEdits, in.TestPkg, in.Run, oracleTimeout)
		return nil, res, err
	}
	if len(in.Edits) > 0 {
		res, err := tree.EphemeralEdits(ctx, in.File, in.Edits, in.TestPkg, in.Run, oracleTimeout)
		return nil, res, err
	}
	res, err := tree.Ephemeral(ctx, in.File, []byte(in.Replacement), in.TestPkg, in.Run, oracleTimeout)
	return nil, res, err
}
