// Package mcpserver serves gomutant over the Model Context Protocol: the
// library's operations as tools, a thin shell exactly like the CLI so the two
// faces cannot drift (spec mcp.md). It inherits the advisory stance whole —
// no tool renders a pass/fail verdict (REQ-result-findings).
package mcpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
	"github.com/greatliontech/gomutant/internal/toolversion"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// clientKeepAliveInterval paces the server's keepalive pings — the
// disconnect detector for in-flight campaigns. It equals the progress
// cadence by coincidence, not by policy: a transport pace and a silence
// bound, deliberately independent; no test tightens it, so it is a
// constant, not a seam.
const clientKeepAliveInterval = 30 * time.Second

// Server is a dir-bound MCP server over the gomutant library.
type Server struct {
	dir            string
	updateDocument func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) (gomutant.Routing, error)

	// mu guards the loaded-tree cache. The cached Tree is read-only after
	// load and served to concurrent tool calls; see loadTreeContext for the
	// reuse constraint.
	mu      sync.Mutex
	tree    *gomutant.Tree
	treeKey string
	vouches []string

	// idleMu guards the in-flight call count and the pending idle
	// release (resident.go): the cached tree is dropped once no call
	// has been in flight for the idle window.
	idleMu    sync.Mutex
	inFlight  int
	idle      *time.Timer
	stopped   bool
	returning bool
	trailing  bool
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

// bankedRunSummary is a cancelled run's summary: the banked state under
// the exit cause, and the audit rate the run measured before the exit
// (REQ-exec-banked-summary; REQ-exec-oracle-run's narrowed-survivor
// clause).
func bankedRunSummary(tallies gomutant.RunTallies, cause string, elapsed time.Duration, sel gomutant.Selection, runID string) gomutant.RunSummary {
	summary := gomutant.SummarizeRun(nil, sel, nil)
	summary.Run = runID
	banked := tallies.Banked(cause, elapsed)
	summary.Banked = &banked
	if tallies.Audit.Narrowed > 0 {
		audit := tallies.Audit
		summary.Audit = &audit
	}
	return summary
}

// updateStore commits through the one store a verb opened: a run's
// per-target commits and its final merge share the store's caches and
// the exemptions record the run was prepared with (a store opened per
// commit would re-read the document and re-judge every row against a
// possibly changed record). The test seam, when set, takes the path.
func (s *Server) updateStore(ctx context.Context, store *gomutant.Store, path string, change func([]gomutant.Finding) ([]gomutant.Finding, error)) (gomutant.Routing, error) {
	if s.updateDocument != nil {
		return s.updateDocument(ctx, path, change)
	}
	return store.Update(ctx, change)
}

// Run serves MCP over stdio until the session ends, and leaves one
// diagnosable line behind whatever ended it (REQ-mcp-exit-log): the
// exit log beside the findings document carries every server line the
// protocol layer emits and a final `exit` line naming the class — the
// host closed the transport, the context ended, the transport failed,
// or the serve path itself panicked — so a disconnect the host reports
// without a cause is attributable afterwards. The host-initiated ends —
// its close, its signal — return nil; a transport end returns an
// ExitError.
func (s *Server) Run(ctx context.Context) error {
	return s.runOn(ctx, &mcp.StdioTransport{})
}

// ExitClass names why the server stopped serving (REQ-mcp-exit-log).
type ExitClass string

const (
	// ExitHostClosed: the host closed the transport (stdin EOF) — the
	// ordinary end of a session.
	ExitHostClosed ExitClass = "host-closed"
	// ExitCancelled: the serve context ended (a signal, a parent's
	// cancellation).
	ExitCancelled ExitClass = "cancelled"
	// ExitTransport: the session ended on a transport or protocol error.
	ExitTransport ExitClass = "transport"
	// ExitPanic: the serve path itself panicked; the line is written and
	// the panic re-raised. A tool handler's panic is not this class: it
	// is recovered on the session's goroutine, logged, and answered as
	// an error with the session serving on.
	ExitPanic ExitClass = "panic"
)

// ExitError is Run's error for a session ended on its transport — the
// one end that is a failure: it names the class and carries the cause,
// and MCPExitCode is the status the command exits with
// (REQ-mcp-exit-log).
type ExitError struct {
	Class ExitClass
	Cause error
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("mcp exit %s: %v", e.Class, e.Cause)
}

func (e *ExitError) Unwrap() error { return e.Cause }

// MCPExitCode is the command's exit status for a session that ended on
// this error: 2, the transport class being the one that returns an
// error at all — the host-initiated ends return none and exit 0, and a
// serve-path panic ends the process on the runtime's own panic exit
// (REQ-mcp-exit-log). Named for the tree so the command's discovery
// never matches a subprocess's own status (os.ProcessState.ExitCode
// rides every exec.ExitError the tree wraps).
func (e *ExitError) MCPExitCode() int { return 2 }

// runOn is Run over any transport — the in-memory one in tests — and
// the one place the exit line is written. The served count lives here,
// per session, so a Server run twice reports each session's own.
func (s *Server) runOn(ctx context.Context, transport mcp.Transport) (err error) {
	// Both boundary lines name the log's own path — the file the logger
	// opened — so an operator who finds either finds the file
	// (REQ-mcp-exit-log).
	logger, logPath, closeLog := s.exitLogger()
	defer closeLog()
	start := time.Now()
	var served atomic.Int64
	exitLine := func(level slog.Level, class ExitClass, cause string, extra ...any) {
		logger.Log(context.Background(), level, "exit", append([]any{"class", string(class), "cause", cause, "served", served.Load(), "uptime", time.Since(start).Round(time.Second).String(), "log", logPath}, extra...)...)
	}
	logger.Info("serve start", "dir", s.dir, "log", logPath, "memory-limit", installCeiling())
	defer s.stopIdle()
	defer func() {
		if r := recover(); r != nil {
			exitLine(slog.LevelError, ExitPanic, fmt.Sprint(r))
			closeLog()
			panic(r)
		}
	}()
	err = s.mcpWith(logger, &served).Run(ctx, transport)
	class, cause := exitClass(err, ctx.Err())
	exitLine(slog.LevelInfo, class, cause, serveErrorField(cause, err)...)
	// The host-initiated ends are never failures: no error, exit 0.
	if class != ExitTransport {
		return nil
	}
	return &ExitError{Class: ExitTransport, Cause: err}
}

// serveErrorField is the exit line's `serve-error` pair: the serve's
// own error when the protocol layer surfaced one and the class
// discarded it, and nothing when it would render as the cause already
// (the transport arm; a cancelled serve answered with the context's
// own error) or there was none. A session error the protocol layer
// itself discards on cancellation never reaches here.
func serveErrorField(cause string, err error) []any {
	if err == nil || err.Error() == cause {
		return nil
	}
	return []any{"serve-error", err.Error()}
}

// exitClass decides a session's end from two observed facts, in order:
// a serve context that has ended is a cancelled serve whatever the
// protocol layer returned first — a host that closes the transport and
// signals together races the two inside the protocol layer, and the
// fact recorded is the context's, not the race's; a clean return under
// a live context is the host's close; anything else is the transport.
func exitClass(err, ctxErr error) (ExitClass, string) {
	switch {
	case ctxErr != nil:
		return ExitCancelled, ctxErr.Error()
	case err == nil:
		return ExitHostClosed, "the host closed the transport"
	default:
		return ExitTransport, err.Error()
	}
}

// exitLogMaxBytes bounds the exit log at one megabyte: a write that
// would carry it past the bound first moves the log to `<name>.1`, one
// generation kept.
const exitLogMaxBytes = 1 << 20

// exitLogger opens the exit log for appending under its size bound and
// returns the logger the protocol layer and the exit line share with
// the path it opened; a log whose home does not resolve, or that is
// unwritable, degrades to a discarding logger with the reason on
// seams.exitLogNotice and no path. The log lives under the tree's
// state home (its directory created by the writer); the server itself
// writes nothing under the served tree — `.gomutant/` appears only
// through a committing verb's own act (REQ-mcp-exit-log).
func (s *Server) exitLogger() (*slog.Logger, string, func()) {
	discard := func() (*slog.Logger, string, func()) {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), "", func() {}
	}
	paths, err := gomutant.ExitLogPaths(s.dir)
	if err != nil {
		fmt.Fprintf(seams.exitLogNotice, "gomutant mcp: exit log home unresolvable (%v); serving without it\n", err)
		return discard()
	}
	f, err := openRotatingFile(paths[0], paths[1], exitLogMaxBytes)
	if err != nil {
		fmt.Fprintf(seams.exitLogNotice, "gomutant mcp: exit log %s unwritable (%v); serving without it\n", paths[0], err)
		return discard()
	}
	return slog.New(slog.NewTextHandler(f, nil)), paths[0], func() { _ = f.Close() }
}

// rotatingFile is the exit log's writer: append-only, bounded at every
// write — a write that would carry the file past the bound first moves it
// to its kept generation (rotated, named at construction) — so one long
// session is bounded exactly as many short ones are. A single write
// larger than the bound lands whole in a fresh file. The size is this
// writer's view: two servers appending to one document's log each count
// their own bytes, so the file may pass the bound by the other's lines
// and one may move the file the other is appending to — a misplaced line,
// never a wrong class. A reopen that fails leaves the writer empty until
// the next write reopens.
type rotatingFile struct {
	path    string
	rotated string
	max     int64
	mu      sync.Mutex
	f       *os.File
	size    int64
}

func openRotatingFile(path, rotated string, max int64) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &rotatingFile{path: path, rotated: rotated, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, info.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && r.size > 0 && r.size+int64(len(p)) > r.max {
		_ = r.f.Close()
		_ = os.Rename(r.path, r.rotated)
		r.f, r.size = nil, 0
	}
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	return r.f.Close()
}

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

// loadTreeReporting is every tool's typed load: `loading` announced at
// once when a token listens (REQ-exec-run-status), then the load under
// the heartbeat.
func (s *Server) loadTreeReporting(ctx context.Context, notify func(string), sel gomutant.Selection) (*gomutant.Tree, error) {
	if notify != nil {
		notify(preparationMessage(loadingEvent))
	}
	label := newStretchLabel(gomutant.StretchPreparing(loadingEvent))
	return withHeartbeatLabel(ctx, notify, label.get, func(ctx context.Context) (*gomutant.Tree, error) { return s.loadTreeContext(ctx, sel) })
}

// loadingEvent is the load's preparation event, the one every verb
// that loads announces and names its stretch by.
var loadingEvent = gomutant.PreparationEvent{Stage: gomutant.PreparationLoading}

// stretchLabel is a heartbeat's label — the stretch a call reports as
// still working — behind the one setter every stretch of the call goes
// through, so a test pins a face's sequence through the stretch
// observer without racing the cadence (REQ-exec-run-status).
type stretchLabel struct{ v atomic.Value }

func newStretchLabel(seed string) *stretchLabel {
	l := &stretchLabel{}
	l.set(seed)
	return l
}

func (l *stretchLabel) set(label string) {
	l.v.Store(label)
	if seams.stretchObserver != nil {
		seams.stretchObserver(label)
	}
}

func (l *stretchLabel) get() string { return l.v.Load().(string) }

// inspectionHeartbeat runs a record walk under the heartbeat: the
// walk's first stage is announced once to a listening token and every
// stage names the heartbeat's stretch, through the one vocabulary the
// CLI's cadence prints (REQ-exec-run-status) — one announcement per
// walk, however many records it judges, so the notification channel
// stays bounded (REQ-mcp-envelope); seed is the label until the walk
// names its first stage — the load's where the call loaded, the
// preparation's where it did not.
func inspectionHeartbeat[T any](ctx context.Context, notify func(string), seed string, walk func(ctx context.Context, phase func(stage string)) (T, error)) (T, error) {
	label := newStretchLabel(seed)
	var announce sync.Once
	return withHeartbeatLabel(ctx, notify, label.get, func(ctx context.Context) (T, error) {
		return walk(ctx, func(stage string) {
			stretch := gomutant.StretchInspecting(stage)
			label.set(stretch)
			if notify != nil {
				announce.Do(func() { notify(stretch) })
			}
		})
	})
}

// withHeartbeat runs fn while a bounded ticker tells a progress-token
// client the labeled stretch is still working - no compile, load, or
// oracle stretch stays silent when the client asked for progress
// (REQ-mcp-envelope). The caller passes its request's ONE notifier so
// every stretch shares one monotonically increasing progress counter -
// MCP requires the value to increase per token, and a fresh counter per
// stretch would regress it. A nil notifier (no token) is exactly fn.
func withHeartbeatLabel[T any](ctx context.Context, notify func(string), label func() string, fn func(context.Context) (T, error)) (T, error) {
	if notify == nil {
		return fn(ctx)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		started := time.Now()
		ticker := time.NewTicker(seams.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				notify(fmt.Sprintf("still working: %s (%s elapsed)%s", label(), time.Since(started).Round(time.Second), residentSuffix()))
			}
		}
	}()
	return fn(ctx)
}

// MCP builds the protocol server (REQ-mcp-tools) outside a session:
// no exit log, the served count nobody reads — the shape the surface
// pins inspect.
func (s *Server) MCP() *mcp.Server {
	return s.mcpWith(slog.New(slog.NewTextHandler(io.Discard, nil)), new(atomic.Int64))
}

// mcpWith is MCP with the session's logger — the protocol layer's own
// lines go to it — and the session's served counter (REQ-mcp-exit-log).
func (s *Server) mcpWith(logger *slog.Logger, served *atomic.Int64) *mcp.Server {
	options := serverOptions()
	options.Logger = logger
	srv := mcp.NewServer(&mcp.Implementation{Name: "gomutant", Version: toolversion.Identity()}, options)
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			// A handler panic runs on the session's goroutine — past the
			// protocol layer, which recovers nothing — so it is caught
			// here: logged with its method and cause, answered as an
			// error, the session continuing (REQ-mcp-exit-log). Only a
			// panic on the serve path itself ends the session. Every
			// tool call answered — the panicked one as an error — then
			// counts once for the exit line and ends as a call for the
			// resident-set policy (the last in-flight call's end returns
			// the heap and arms the idle release — REQ-mcp-resident-set):
			// a receiving middleware, so no handler carries either duty.
			if method == "tools/call" {
				s.callBegan()
			}
			defer func() {
				if r := recover(); r != nil {
					logger.Error("handler panic", "method", method, "cause", fmt.Sprint(r))
					result, err = nil, fmt.Errorf("gomutant: %s panicked: %v", method, r)
				}
				if method == "tools/call" {
					served.Add(1)
					s.callEnded()
				}
			}()
			return next(ctx, method, req)
		}
	})
	mcp.AddTool(srv, knobbedTool[runIn]("run"), s.toolRun)
	mcp.AddTool(srv, knobbedTool[discoverIn]("discover"), s.toolDiscover)
	mcp.AddTool(srv, knobbedTool[findingsIn]("findings"), s.toolFindings)
	mcp.AddTool(srv, knobbedTool[explainIn]("explain"), s.toolExplain)
	mcp.AddTool(srv, knobbedTool[attestIn]("attest_survivor"), s.toolAttest)
	mcp.AddTool(srv, knobbedTool[pruneIn]("prune"), s.toolPrune)
	mcp.AddTool(srv, knobbedTool[retargetIn]("retarget"), s.toolRetarget)
	mcp.AddTool(srv, knobbedTool[ephemeralIn]("ephemeral"), s.toolEphemeral)
	// guidance serves embedded content and touches no tree state.
	mcp.AddTool(srv, knobbedTool[guidanceIn]("guidance"), s.toolGuidance)
	return srv
}

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
	return "prepare " + event.Text()
}

func decisionMessage(decision gomutant.RunDecision) string {
	label, rest := decision.Text()
	return "decision " + label + " " + rest
}

// localPath refuses a tree-relative input that escapes the server's dir —
// the surface is dir-bound, and an escaping ephemeral file would no-op in
// the overlay and read as a survivor.
func localPath(name, p string) error {
	if p == "" {
		return nil
	}
	_, err := gomutant.ConfineToTree(name, "", p)
	return err
}

// selectionIn is the build-selection surface every tree-consuming tool
// shares: declared tags and a toolchain directive rewrite the tree's
// one frozen environment at load, so discovery, resolution, oracle
// spawns, and the measurement pins all see the same selection by
// construction (a //go:build-gated oracle measures exactly as an
// untagged one).
type selectionIn struct {
	Tags      []string `json:"tags,omitempty"`
	Toolchain string   `json:"toolchain,omitempty"`
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

// oracleExecutionPolicyOut reports a recorded measurement's policy without
// judging the current tree. Passing this gate alone establishes no freshness.
type oracleExecutionPolicyOut struct {
	OracleExecutionPolicy      string `json:"oracleExecutionPolicy,omitempty" jsonschema:"the recorded oracle execution policy; absent on legacy records, never a current-tree freshness claim"`
	OracleExecutionPolicyIssue string `json:"oracleExecutionPolicyIssue,omitempty" jsonschema:"the measurement policy's known limitation; when present the counts are historical, not complete-oracle evidence; absence only permits further evidence judgment"`
}

func oracleExecutionPolicyRow(f gomutant.Finding) oracleExecutionPolicyOut {
	return oracleExecutionPolicyOut{
		OracleExecutionPolicy:      f.OracleExecutionPolicy,
		OracleExecutionPolicyIssue: gomutant.OracleExecutionPolicyIssue(f),
	}
}

// envelope is the response envelope's one bounding policy
// (REQ-mcp-envelope): every cap a response applies is a field here,
// its purpose stated once, and every capped list counts its omitted
// remainder. Never written — a variable only because Go has no
// constant structs.
var envelope = struct {
	// rows bounds every response row list — targets, residue, finding
	// and inspection rows, guidance, sheds and carries, contradictions,
	// property-oracle statements, oracle sets, explain's groups, the
	// findings response's ephemeral attestations. The served prose
	// (tool descriptions, the guidance document) states this number;
	// the requirement names it, and the policy pin holds them together.
	rows int
	// open bounds the open survivors listed per record.
	open int
	// reasons bounds the machine-local clauses listed per record.
	reasons int
	// nested bounds a row's own nested lists — a guidance row's targets
	// and unstable tests, an explain group's symbols — where one row
	// would otherwise carry an unauthored blow-up.
	nested int
	// streamed bounds the preparation events and decisions kept inline
	// for a request without a progress token (a token receives them all
	// as notifications); the payload-bearing analysis events kept inline
	// the same way take the row bound — each carries up to a
	// diagnostic's worth of the oracle's own output.
	streamed int
}{rows: 50, open: 20, reasons: 20, nested: 10, streamed: 100}

// capRows bounds a response list at the envelope cap with the remainder
// counted (REQ-mcp-envelope); the findings document carries every full
// set a capped row points at. The returned slice is capacity-clipped so
// a later append can never scribble over the caller's retained full
// list.
func capRows[T any](rows []T) ([]T, int) {
	if len(rows) <= envelope.rows {
		return rows, 0
	}
	return rows[:envelope.rows:envelope.rows], len(rows) - envelope.rows
}

// targetInputs are the wire's target sources as given: the document
// path with the tree as its root (confined and resolved by the
// preparation, REQ-mcp-envelope), the inline document's bytes, a
// changed ref through the git seam — read at their enumerated places.
func (s *Server) targetInputs(targetsPath, targetsJSON, changed string) gomutant.TargetInputs {
	in := gomutant.TargetInputs{TargetsPath: targetsPath, TargetsRoot: s.dir}
	if targetsJSON != "" {
		in.TargetsJSON = []byte(targetsJSON)
	}
	if changed != "" {
		in.Changed = gitref.ChangedSelection(s.dir, changed)
	}
	return in
}

// selectTargets resolves a prepared request through the library's one
// dispatch (REQ-target-filtering, REQ-mcp-envelope).
func (s *Server) selectTargets(ctx context.Context, tree *gomutant.Tree, request gomutant.SelectionRequest) (gomutant.TargetSelection, error) {
	if seams.selectionObserver != nil {
		seams.selectionObserver()
	}
	return tree.SelectTargets(ctx, request)
}

// mcpOracleMemoryBytes converts the optional MiB input: absent is the
// derived default, the rest the one MiB policy both faces share.
func mcpOracleMemoryBytes(mib *int64) int64 {
	if mib == nil {
		return 0
	}
	return gomutant.OracleMemoryBytesFromMiB(*mib)
}

// guidanceOrientation is the decision map the served instructions and
// the bare guidance call open with.
func guidanceOrientation() string { return gomutant.Guidance().Orientation() }

// guidanceIn asks for one verb's section or, empty, the decision map.
type guidanceIn struct {
	Verb string `json:"verb,omitempty"`
}

// toolGuidance serves the embedded guidance document
// (REQ-mcp-guidance): a verb's full section under its mcp spelling,
// or the decision map for orientation. It touches no tree state.
func (s *Server) toolGuidance(ctx context.Context, req *mcp.CallToolRequest, in guidanceIn) (*mcp.CallToolResult, any, error) {
	if in.Verb == "" {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: gomutant.Guidance().Orientation()}}}, nil, nil
	}
	long, err := gomutant.Guidance().Long("mcp", in.Verb)
	if err != nil {
		return nil, nil, fmt.Errorf("%w; empty verb serves the decision map, which names every verb", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: long}}}, nil, nil
}

// wireTargetSources names a call's given target sources in the wire's
// spelling — the spellings' one home on this face.
func wireTargetSources(targetsPath, targetsJSON, changed string) []string {
	return gomutant.TargetSourcesGiven(
		gomutant.TargetSource{Name: "targets_path", Given: targetsPath != ""},
		gomutant.TargetSource{Name: "targets_json", Given: targetsJSON != ""},
		gomutant.TargetSource{Name: "changed", Given: changed != ""})
}
