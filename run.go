package gomutant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/trace"
)

var findingObservationSequence atomic.Uint64

// runOptions is the run's own view of its options: the caller's
// Options beside the services Run installs — the machine-local
// baseline bank, the derived budget, leash,
// and duration resolvers, and the producer-probe gate. Every callee
// below Run takes this, never a bare Options, so a caller-constructed
// Options with no services cannot reach the execution machinery and
// read as "no gating, no bank": nil-service semantics
// are unrepresentable there — Run is the one constructor. Callbacks
// stay on Options: they are the caller's surface, locked once.
type runOptions struct {
	Options
	// baselineBank is the run's machine-local measurement bank
	// (REQ-result-baseline-bank); nil disables banking.
	baselineBank *baselineBank
	// groupBudget resolves an oracle group's effective budget: the
	// group's derived budget in derive mode, the explicit timeout
	// otherwise; nil (probes) means OracleTimeout.
	groupBudget func(g group) time.Duration
	// baselineDur answers a group's measured passing-baseline
	// wall-clock — the window cost model's whole-group price; nil or
	// a false answer prices nothing, never fabricates
	// (REQ-exec-run-status's estimate class).
	baselineDur func(g group) (time.Duration, bool)
	// groupLeash resolves the leash a group's advisory probes run
	// under — the campaign leash lifted by the group's baseline in
	// derive mode, the explicit timeout otherwise; nil before the run
	// installs it (advisoryLeash falls back to OracleTimeout).
	groupLeash func(g group) time.Duration
	// probeGate is the run's producer-probe gate: producer-side
	// oracle probes hold it shared so they never share a window with a serial
	// confirmation's scored run (REQ-exec-attribution).
	probeGate *sync.RWMutex
	// foldedCoverage answers a group's baseline coverage when the
	// initial baseline probe folded -coverprofile over coverPkg
	// (REQ-exec-survivor-evidence).
	foldedCoverage func(g group, coverPkg string) (engine.Coverage, bool)
	// bounds are the run's oracle resource bounds, derived once at
	// Run's entry and applied to every oracle spawn.
	bounds engine.OracleBounds
}

// advisoryLeash is the bound an advisory probe of g runs under
// (groupLeash when the run installed it, the run-wide bound otherwise).
func (o runOptions) advisoryLeash(g group) time.Duration {
	if o.groupLeash != nil {
		return o.groupLeash(g)
	}
	return o.OracleTimeout
}

// lockCallbacks wraps every caller callback in one mutex so pipelined
// preparation and execution — which invoke callbacks from two goroutines —
// preserve the synchronous-caller-code contract: no two callbacks ever run
// concurrently (REQ-exec-run-status). AnalysisEvent is exempt by its own
// documented contract (safe for concurrent invocation).
func lockCallbacks(opts Options) Options {
	var mu sync.Mutex
	if fn := opts.Guidance; fn != nil {
		opts.Guidance = func(g OracleGuidance) { mu.Lock(); defer mu.Unlock(); fn(g) }
	}
	if fn := opts.PropertyOracle; fn != nil {
		opts.PropertyOracle = func(n PropertyOracleNote) { mu.Lock(); defer mu.Unlock(); fn(n) }
	}
	if fn := opts.Decision; fn != nil {
		opts.Decision = func(d RunDecision) { mu.Lock(); defer mu.Unlock(); fn(d) }
	}
	if fn := opts.Executing; fn != nil {
		opts.Executing = func(e ExecutionEvent) { mu.Lock(); defer mu.Unlock(); fn(e) }
	}
	if fn := opts.Progress; fn != nil {
		opts.Progress = func(e PreparationEvent) { mu.Lock(); defer mu.Unlock(); fn(e) }
	}
	if fn := opts.Commit; fn != nil {
		opts.Commit = func(f Finding) error { mu.Lock(); defer mu.Unlock(); return fn(f) }
	}
	if fn := opts.Contradiction; fn != nil {
		opts.Contradiction = func(c AttestationContradiction) { mu.Lock(); defer mu.Unlock(); fn(c) }
	}
	if fn := opts.AttestationSiteShed; fn != nil {
		opts.AttestationSiteShed = func(d AttestationShed) { mu.Lock(); defer mu.Unlock(); fn(d) }
	}
	if fn := opts.AttestationCarried; fn != nil {
		opts.AttestationCarried = func(c AttestationCarry) { mu.Lock(); defer mu.Unlock(); fn(c) }
	}
	if fn := opts.afterExecution; fn != nil {
		opts.afterExecution = func() { mu.Lock(); defer mu.Unlock(); fn() }
	}
	if fn := opts.aggregate; fn != nil {
		opts.aggregate = func() { mu.Lock(); defer mu.Unlock(); fn() }
	}
	if fn := opts.producer; fn != nil {
		opts.producer = func(s string) { mu.Lock(); defer mu.Unlock(); fn(s) }
	}
	if fn := opts.proofAttempt; fn != nil {
		opts.proofAttempt = func(s string, attempt int) { mu.Lock(); defer mu.Unlock(); fn(s, attempt) }
	}
	if fn := opts.dispatched; fn != nil {
		opts.dispatched = func(s string, mi int) { mu.Lock(); defer mu.Unlock(); fn(s, mi) }
	}
	if fn := opts.executedScope; fn != nil {
		opts.executedScope = func(pos, op string, scope []string) { mu.Lock(); defer mu.Unlock(); fn(pos, op, scope) }
	}
	if fn := opts.confirmScoped; fn != nil {
		opts.confirmScoped = func(pos, op string, scope []string) { mu.Lock(); defer mu.Unlock(); fn(pos, op, scope) }
	}
	return opts
}

// NewRunID mints a run identity: 16 hex characters of entropy, opaque
// and compared for equality only (Finding.Run).
func NewRunID() string {
	var b [8]byte
	rand.Read(b[:]) // never fails: the runtime aborts the process if the OS entropy source does
	return hex.EncodeToString(b[:])
}

// validateRunID refuses a caller-supplied identity the persisted document
// and the line-oriented faces cannot carry verbatim: 1-64 bytes of
// letters, digits, `.`, `_`, `-`.
func validateRunID(id string) error {
	if len(id) == 0 || len(id) > 64 {
		return fmt.Errorf("gomutant: run identity %q refused: want 1-64 bytes", id)
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return fmt.Errorf("gomutant: run identity %q refused: want letters, digits, '.', '_', '-'", id)
		}
	}
	return nil
}

// Options bound a run.
type Options struct {
	// Budget caps selected candidates per symbol; 0 means all (REQ-mut-budget).
	Budget int
	// RunID is the run's identity, stamped on every record the run
	// measures (Finding.Run); empty mints one (NewRunID). A face mints
	// its own so it can name the run before the first decision.
	RunID string
	// OracleTimeout bounds each oracle process as a uniform explicit
	// pin; 0 derives each oracle group's budget from that group's own
	// measured baseline (a multiple with a 5s floor), the finding
	// marking the derived posture (REQ-exec-oracle-run's derived
	// campaign budget).
	OracleTimeout time.Duration
	// AnalysisBudget bounds each freshness-proof pass's precise analysis
	// by wall clock — the observed union's proof capture and every
	// producer validation (REQ-exec-analysis-budget): a subject the
	// budget left unproven lands an unavailable observation proof
	// naming the budget, the target's measurement stands (a subject
	// whose reuse rests on the proof re-executes before reuse), and
	// the run never stalls in a proof pass; a validation the budget cut stamps the finding's evidence
	// unverifiable instead of refusing the target. Zero, the default,
	// is unbounded; negative is refused before the load.
	AnalysisBudget time.Duration
	// SoftStop, when non-nil and closed, interrupts the campaign
	// GRACEFULLY: no further mutants are admitted, in-flight mutants
	// finish and their kills confirm serially, and each fresh-measure
	// target whose candidate prefix drained commits as an ordinary
	// candidate-capped record — Budget and Generated at the measured
	// prefix — whose reuse follows REQ-result-stale's budget-extension
	// carve-out, so re-running the same command measures only the
	// remainder. In-flight serve, splice, and drift work discards
	// whole (its prior records stand). The run still returns an
	// operational cancellation error; a context cancellation remains
	// the hard stop that discards unfinished work whole
	// (REQ-exec-cancellation's graceful-interrupt clause).
	SoftStop <-chan struct{}
	// OracleMemoryBytes ceilings each oracle process tree's memory
	// (REQ-exec-oracle-memory): 0 derives RAM/(2 x jobs) floored at
	// 1 GiB, negative disables. A runaway-allocation mutant dies on its
	// own ceiling as an ordinary kill instead of OOMing the host.
	OracleMemoryBytes int64
	// Force re-measures targets whose prior finding's pins still match.
	Force bool
	// Guidance receives oracle-instability attribution for a measured
	// target whose merged runtime evidence landed unverifiable under a
	// package-derived oracle: each oracle test probed individually, the
	// unstable ones named with a narrowing suggestion
	// (REQ-exec-oracle-guidance). Nil skips the attribution probes.
	Guidance func(OracleGuidance)
	// PropertyOracle states a property-runtime prerequisite discovered
	// in an oracle package before execution: what the run pinned itself,
	// or what the caller must ensure (REQ-exec-property-oracles).
	PropertyOracle func(PropertyOracleNote)
	// BracketPaths declares external surfaces the oracle legitimately
	// reads — tree-relative paths (a file or a directory tree),
	// resolved against the tree root as one declared surface for every
	// module's oracles, or absolute files or directories outside the
	// tree — extending each spawn's observation bracket beyond the
	// oracle package directory (REQ-exec-observation). A relative path
	// escaping the tree, or one under a tool-excluded directory, is
	// refused at run start. Declaring a path carries the bracket
	// contract's mutation-free assertion for the span.
	BracketPaths []string
	// Staged pins the run to the git index snapshot
	// (REQ-result-staged): staged-but-uncommitted content is the
	// measured subject and counts clean; unstaged drift over a
	// measured package's inputs refuses that target instead of
	// stamping dirty machine-local evidence, and the finding records
	// the index tree identity the eventual commit will carry.
	Staged bool
	// OwnWrites lists tree paths the caller itself writes during the
	// run — its findings document, the campaign lock, their siblings —
	// excluded from measurement-residue attribution: the residue
	// provenance names the measured code's tree writes
	// (REQ-exec-quiescence), and a harness artifact attributed there
	// makes every default-layout run read as self-inflicted drift.
	OwnWrites []string
	// Exemptions is the committed exemption record
	// (REQ-result-exemptions): reviewed acceptances of exactly-named
	// unverifiable reasons per subject, consumed by survivor bucketing
	// and stamped onto each finding they cover; the persistence layer
	// re-reads the record itself, so revocation bites every later
	// classification.
	Exemptions []Exemption
	// ScratchNamespaces declares in-tree run-scratch namespaces for
	// observation ingest (REQ-exec-scratch-namespace): each is a
	// tree-relative directory plus a single-component os.MkdirTemp
	// name pattern, becoming a gofresh scratch namespace
	// (REQ-inputs-scratch-namespace) over every oracle observation. The
	// declaration forfeits exactly the appearance-pin of absence-probes
	// matching the pattern inside the directory - the caller-side
	// assertion its author takes on. Malformed declarations refuse at
	// run start, before any measurement.
	ScratchNamespaces []gofresh.ScratchNamespace
	// Jobs bounds concurrent mutant runs; 0 means half the CPUs. Mutant runs
	// are process-isolated (own overlay, own temp dir, shared
	// content-addressed build cache), so they parallelize safely — but
	// load-induced flakes read as kills, so the default hedges.
	Jobs int
	// Prior findings (a parsed document): a target whose pins all hold is
	// served from here instead of re-measured (REQ-result-stale).
	Prior []Finding
	// Decision receives each target's deterministic disposition, streaming
	// in target order as each target's preparation completes — before that
	// target's own mutants execute, possibly after earlier targets began
	// executing (REQ-exec-run-status).
	Decision func(RunDecision)
	// Posture receives, once the run's records are final, each record's
	// reuse posture — measured, cached, or skipped; reusable as it
	// stands or not, with every refusing channel named and what would
	// lift it — judged over the views the run already built, so a
	// consumer never mistakes a committed record for reusable evidence
	// and never needs a second operation to learn it is not
	// (REQ-result-run-posture). Nil asks for no posture.
	Posture func(RecordPosture)
	// Tallies receives what the run counted about itself — the banked
	// state and retained historical audit fields — on every exit once measurement began
	// (the first execution event, the first window's dispatch) and on
	// every completed or drifted run; a run cancelled before that calls
	// it never (REQ-exec-banked-summary).
	Tallies func(RunTallies)
	// Layer classifies a committed finding by the layer its commit
	// landed it in — the store's own predicate, LayerRepo or LayerLocal
	// with the disqualifying reason — so the banked tallies count what
	// the findings document holds apart from what stayed machine-local
	// (REQ-exec-banked-summary, REQ-result-layers). Nil counts every
	// commit as the document's.
	Layer func(Finding) (layer, reason string)
	// PlanOnly stops the run after the deterministic preparation sequence
	// and target decisions: mutants are enumerated and every decision is
	// computed and delivered, but no baseline probes, no mutant executes,
	// and nothing new is persisted (cached serves already committed are
	// idempotent re-merges of existing records). The return carries only
	// the findings complete without execution — cached serves and skips —
	// so the decisions are the plan and precondition holes surface before
	// any budget is spent (REQ-exec-run-status's plan clause).
	PlanOnly bool
	// PlanOnly also suppresses Commit at the library boundary — the
	// no-write guarantee is the run's, not each caller's to re-implement.
	// Executing must be safe for synchronous invocation from the run loop
	// and receives advisory execution-phase progress: which target window
	// is dispatching or confirming, exact candidate tallies over the
	// targets prepared so far (growing to campaign-wide as pipelined
	// preparation completes), and per-kill confirmation progress while
	// confirming.
	// Events are diagnostic, carry no ordering or completion
	// guarantee beyond target-window boundaries, and never enter a
	// decision or finding (REQ-exec-run-status's advisory classes). The
	// callback must return normally.
	Executing func(ExecutionEvent)
	// Progress synchronously receives deterministic preparation events; a
	// target's own events precede its decision and its execution, while
	// preparation pipelined behind earlier targets' execution may interleave
	// with their advisory execution events. It must return normally
	// (REQ-exec-run-status).
	Progress func(PreparationEvent)
	// AnalysisEvent must be safe for concurrent invocation and
	// synchronously receives the run's freshness-analysis events: the
	// gofresh phase name, the package for per-package phases, and a
	// non-empty detail for payload-bearing diagnostics (the
	// per-subject analysis-unavailable provenance, the unlisted-
	// toolchain notice). Detail-free events are advisory keep-alives a
	// consumer may throttle; detail-bearing events are each a distinct
	// fact the operator needs — never throttle or fold them, and the
	// package stays a package. One field by design, mirroring
	// gofresh's own single Progress channel: a consumer cannot
	// subscribe to the keep-alives without the diagnostics (a
	// two-field shape shipped exactly that wiring gap). Events are
	// diagnostic, carry no completion signal, never enter a decision
	// or finding, and their sequence is not part of the deterministic
	// run-status contract (REQ-exec-run-status). The callback must
	// return normally.
	AnalysisEvent func(AnalysisEvent)
	// Commit synchronously receives each finished target's final finding —
	// a cached serve as soon as its pins are proven to hold, a measured or
	// spliced target after its post-execution producer validation — so the
	// caller can persist completed targets incrementally and an interrupted
	// run keeps every finding committed before cancellation became observable
	// (REQ-exec-cancellation). The caller's final merge of the returned
	// findings remains the authority; re-merging a committed finding is
	// idempotent. A returned error aborts the run. Skipped targets measure
	// nothing and are never delivered. Like every callback, it may run on
	// the run's preparation goroutine: a callback that panics is
	// process-fatal rather than an unwind Run's caller can recover.
	Commit         func(Finding) error
	afterExecution func()
	aggregate      func()
	producer       func(string)
	// proofAttempt observes each freshness-proof construction attempt —
	// (unit, 1) before a proof unit's pass, the unit named by its oracle
	// packages joined with ",", then (symbol, 2) before a faulted
	// target's bounded per-target retry — a test seam pinning the
	// unit/retry split, like producer above.
	proofAttempt func(symbol string, attempt int)
	// Contradiction receives each attested survivor a new measurement
	// killed: the attestation is shed — evidence beats
	// attestation — and the contradiction is worth a human's attention,
	// because a mutant judged equivalent was just distinguished
	// (REQ-attest-survivor). Nil drops the reports; the
	// shed itself is unconditional.
	Contradiction func(AttestationContradiction)
	// AttestationSiteShed reports a disposition refused only because the
	// site content under its position changed - surfaced, never silent
	// (REQ-attest-survivor).
	AttestationSiteShed func(AttestationShed)
	// AttestationCarried reports a disposition carried across moved
	// measurement pins: the mutation domain held and the mutant survived
	// re-execution under the current pins, so the reasoning rides - an
	// acceptance outliving the environment it was judged in, auditable at
	// the moment it rides (REQ-attest-survivor). Nil drops the reports;
	// the carry itself is unconditional. A pins-held carry is not
	// reported - it restores the record verbatim.
	AttestationCarried func(AttestationCarry)
	// dispatched observes each candidate index handed to the worker pool —
	// a test seam pinning suffix-only dispatch under a budget extension,
	// flagged-only dispatch under a candidate-evidence serve, and complete
	// dispatch under changed oracle evidence. Deterministically ordered.
	dispatched func(symbol string, mi int)
	// executedScope observes each mutant execution's oracle scope
	// (per-group -run patterns), keyed by candidate identity (position,
	// operator) — emitted by the executor off the same work value whose
	// groups execute verbatim. Unordered (workers race); pins the scope
	// of the complete current oracle.
	executedScope func(position, operator string, oracleScope []string)
	// confirmScoped observes each serial confirmation's candidate group
	// scope the same way, emitted at the confirmation executor's own
	// entry off its w: the serial fallback executes those groups
	// verbatim, and the killer-scoped branch runs its killer sub-probe
	// within one of them — pinning both confirmation routes to the
	// candidate's complete current oracle.
	confirmScoped func(position, operator string, oracleScope []string)
}

// PreparationStage identifies one observable pre-execution operation.
type PreparationStage string

const (
	PreparationLoading   PreparationStage = "loading"
	PreparationResolving PreparationStage = "resolving"
	PreparationFreshness PreparationStage = "freshness"
	// PreparationViews prices a mode's decision-view build before it is
	// paid — the distinct subjects requested and the packages they
	// span — and PreparationProofs prices the mode's observed union the
	// same way before its proof pass, over the subjects that hold a
	// decision view (a requested subject that faulted out of the build
	// is in the first count and not the second); a reader sees the cost
	// of the run's longest silent stretches before entering them
	// (REQ-exec-run-status).
	PreparationViews    PreparationStage = "views"
	PreparationProofs   PreparationStage = "proofs"
	PreparationMutants  PreparationStage = "mutants"
	PreparationBaseline PreparationStage = "baseline"
	// PreparationOracleBudget reports an oracle group's derived budget
	// the moment its passing baseline measures it (REQ-exec-oracle-run's
	// derived campaign budget) — the campaign face's parity with the
	// ephemeral face's effective-budget report.
	PreparationOracleBudget PreparationStage = "oracle-budget"
	// PreparationMutantRun precedes each of an ephemeral probe's mutant
	// runs (Symbol carries "i/N"); PreparationCoverage precedes its
	// advisory coverage probe (REQ-exec-run-status's ephemeral phases).
	PreparationMutantRun PreparationStage = "mutant-run"
	PreparationCoverage  PreparationStage = "coverage"
)

// ExecutionEvent is one advisory execution-phase progress report: the
// window's phase (executing or confirming), the 1-based index of the
// window's first measure target among those dispatched and the count of
// measure targets prepared so far, exact candidate tallies over the
// prepared targets (carried and non-runnable candidates included — the
// plan's own counting — growing to campaign-wide as pipelined
// preparation completes), and, while confirming, the
// window's serial confirmation progress. Advisory only —
// timing-dependent, outside the deterministic run-status sequence.
type ExecutionEvent struct {
	Phase           string `json:"phase"`
	TargetIndex     int    `json:"targetIndex"`
	TargetCount     int    `json:"targetCount"`
	Symbol          string `json:"symbol"`
	CandidatesDone  int    `json:"candidatesDone"`
	CandidatesTotal int    `json:"candidatesTotal"`
	// ConfirmationsDone/Total report the window's serial kill
	// confirmation progress; zero totals outside confirming phases.
	// Total is the upper bound (every confirmable kill) — the stride
	// gate finishes below it in every clean window.
	ConfirmationsDone  int `json:"confirmationsDone,omitempty"`
	ConfirmationsTotal int `json:"confirmationsTotal,omitempty"`
	// ConfirmationMode names the gate state deciding THIS confirmation:
	// serial-full while the gate confirms every kill (opening streak,
	// volatile evidence, or a flip re-arming the window), stride-sampled
	// once the streak has earned sampling. Set on confirming events
	// only — the disarmed-stride state is otherwise indistinguishable
	// from the armed one in the log (the field report's ask).
	ConfirmationMode string `json:"confirmationMode,omitempty"`
	// FlipPosition/FlipKiller are set exactly on a confirmation-flip
	// event: the serial re-run re-scored a provisional kill (initially
	// attributed to FlipKiller) as a survivor. A demotion is never
	// silent — the flip also rides the RECORD as the survivor's
	// flipped-kill bucket and WithdrawnKiller
	// (REQ-exec-survivor-evidence), so a false-survivor triage is
	// self-diagnosing from the findings document, the event stream
	// being corroboration.
	FlipPosition string `json:"flipPosition,omitempty"`
	FlipKiller   string `json:"flipKiller,omitempty"`
	// AuditedNarrowed/AuditDisagreed describe historical audit events.
	// Complete-oracle execution emits neither audit nor audit-flip.
	AuditedNarrowed int `json:"auditedNarrowed,omitempty"`
	AuditDisagreed  int `json:"auditDisagreed,omitempty"`
	// The estimate event (Phase "estimate", one per window before
	// dispatch) carries the window cost model: EstimateProjected is the
	// priced PROJECTION of complete-group
	// oracle time at measured-baseline pace (absent when nothing
	// priced) — a pace anchor, not a bound in either direction: a
	// timing-out candidate costs up to its derived budget, and
	// confirmations, bucket probes, and build overlay are excluded —
	// and EstimateFull/EstimateUnknown classify the executing
	// candidates — unpriced candidates are NEVER folded into the
	// projection, they are counted (REQ-exec-run-status's estimate
	// class). Phase "tick" events carry per-candidate completion:
	// CandidatesDone advances monotonically as workers finish,
	// truing up to the prepared totals at window boundaries — the
	// pace ground for estimated-remaining, advisory like every
	// execution-phase event.
	EstimateProjected string `json:"estimateProjected,omitempty"`
	// EstimateAudit and EstimateNarrowed are historical event fields;
	// fresh estimates leave them zero.
	EstimateAudit    string `json:"estimateAudit,omitempty"`
	EstimateNarrowed int    `json:"estimateNarrowed,omitempty"`
	EstimateFull     int    `json:"estimateFull,omitempty"`
	EstimateUnknown  int    `json:"estimateUnknown,omitempty"`
	// EstimateNoSignal is the historical scheduling-reason roster.
	// Complete-oracle estimates have no coverage-scheduling signal.
	EstimateNoSignal []string `json:"estimateNoSignal,omitempty"`
	// ProbesDone/ProbesTotal are historical scheduling-probe counts.
	// Fresh runs pay only the separate full-pattern advisory coverage.
	ProbesDone  int `json:"probesDone,omitempty"`
	ProbesTotal int `json:"probesTotal,omitempty"`
	// ProbesUnpriced is the historical unpriced scheduling-batch count.
	ProbesUnpriced int `json:"probesUnpriced,omitempty"`
}

// ProgressCadence is the one cadence every face's progress keeps: the
// CLI's progress line (its operator flag defaults to it) and the MCP
// heartbeat pace on it, so no stretch on either face stays silent
// longer than this (REQ-exec-run-status; the MCP heartbeat clause). A
// fixed value rather than one derived from the measured pace: the
// line's information changes at commits, and a reader — human or
// token-paying — is served by a bounded silence, not by more lines.
const ProgressCadence = 30 * time.Second

// PreparationEvent reports one operation before it begins. Symbol is set for
// target-scoped operations; Package is additionally set for baseline probes.
type PreparationEvent struct {
	Stage   PreparationStage `json:"stage"`
	Symbol  string           `json:"symbol,omitempty"`
	Package string           `json:"package,omitempty"`
	// OracleBudget carries the group's derived oracle budget on
	// PreparationOracleBudget events.
	OracleBudget string `json:"oracleBudget,omitempty"`
	// Banked marks a baseline event served from the machine-local
	// measurement bank instead of probed — the pins re-verified and
	// the observation re-entered through adoption
	// (REQ-result-baseline-bank).
	Banked bool `json:"banked,omitempty"`
	// Subjects and Packages price a pass on PreparationViews and
	// PreparationProofs events: the subjects the pass covers and the
	// distinct packages they span.
	Subjects int `json:"subjects,omitempty"`
	Packages int `json:"packages,omitempty"`
}

// pricedPass builds a pass-pricing event over the symbols the pass
// covers: the distinct subjects (an oracle shared by two targets is one
// subject of the pass) and their distinct packages, named by the
// tree's package resolution.
func pricedPass(stage PreparationStage, symbols []string, packageOf func(string) string) PreparationEvent {
	subjects, packages := map[string]bool{}, map[string]bool{}
	for _, symbol := range symbols {
		subjects[symbol] = true
		packages[packageOf(symbol)] = true
	}
	return PreparationEvent{Stage: stage, Subjects: len(subjects), Packages: len(packages)}
}

// Text renders the event as both faces print it after their own
// prefix: the stage, then the symbol, package, and oracle budget it
// carries, "(banked)" for a baseline served from the bank, and a
// priced pass's subjects and packages.
func (e PreparationEvent) Text() string {
	parts := []string{string(e.Stage)}
	for _, part := range []string{e.Symbol, e.Package, e.OracleBudget} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if e.Banked {
		parts = append(parts, "(banked)")
	}
	if e.Subjects > 0 {
		parts = append(parts, countNoun(e.Subjects, "subject")+" over "+countNoun(e.Packages, "package"))
	}
	return strings.Join(parts, " ")
}

// RunDecision explains whether one target is cached, skipped, or measured.
type RunDecision struct {
	Symbol     string `json:"symbol"`
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
}

// RecordPosture is one record's standing at the end of a run: whether
// this run measured it, served it, or skipped it; whether the record
// is reusable as it stands (the inspection state); each channel that
// refuses reuse with its reason; and what lifts the refusal. The faces
// add the record's layer from their store (REQ-result-run-posture).
type RecordPosture struct {
	Symbol string `json:"symbol"`
	// Measurement is "measured", "cached", or "skipped" for a run's
	// record, "recorded" for an existing record a verdict inspects.
	Measurement string `json:"measurement"`
	// Reuse is the record's inspection state — current, stale,
	// unverifiable, or detached; empty for a skipped record.
	Reuse FindingState `json:"reuse,omitempty"`
	// Reasons names each channel refusing reuse: "freshness" (the
	// judge's verdict against the current tree), "candidate evidence"
	// (flagged candidates), "runtime inputs" (an unverifiable input
	// manifest), and "stored observation" (the capture-time
	// observation the record carries) — the last stated beside the
	// others whenever reuse is refused, it holds, and its text is not
	// the judgment's; a current record carrying one is not refused by
	// it. The channels compose: flagged candidates add theirs beside a
	// freshness refusal.
	Reasons []PostureReason `json:"reasons,omitempty"`
	// Analysis says what a later judgment needs: nothing for a
	// current record; a re-measure for a stale or detached one; a
	// re-execution of the affected evidence for candidate evidence or
	// runtime inputs; for a freshness refusal, that no re-judgment
	// lifts it — a source change, a vouch, or a discharge does; and a
	// re-judgment once the tree loads when the judgment itself failed.
	Analysis string `json:"analysis,omitempty"`
}

// Line is the posture's one human line: the reuse state, each
// refusing channel with its reason, and the analysis.
func (p RecordPosture) Line() string {
	if p.Measurement == "skipped" {
		return "skipped"
	}
	parts := []string{string(p.Reuse)}
	for _, r := range p.Reasons {
		parts = append(parts, r.Channel+": "+r.Reason)
	}
	if p.Analysis != "" {
		parts = append(parts, "analysis: "+p.Analysis)
	}
	return strings.Join(parts, "; ")
}

// RecordedPosture is an existing record's posture outside a run — the
// attestation verdict's — from the inspection the face made once, or
// its failure.
func RecordedPosture(f Finding, inspection FindingInspection, judgeErr error) RecordPosture {
	p := posture(f, inspection, judgeErr)
	p.Measurement = "recorded"
	return p
}

// PostureReason is one refusing channel and its reason.
type PostureReason struct {
	Channel string `json:"channel"`
	Reason  string `json:"reason"`
}

// Posture channel names.
const (
	PostureFreshness         = "freshness"
	PostureCandidateEvidence = "candidate evidence"
	PostureRuntimeInputs     = "runtime inputs"
	PostureStoredObservation = "stored observation"
)

// Posture analyses.
const (
	AnalysisRemeasure = "re-measure"
	AnalysisReexecute = "the next run re-executes the affected evidence"
	AnalysisSource    = "no re-judgment lifts it — a source change, a vouch, or a discharge does"
	AnalysisJudge     = "re-judge once the tree loads"
)

// posture derives one non-skipped record's posture from its inspection
// and its own evidence. The refusing channels compose: the deciding
// check's channel carries the inspection's reason; flagged candidates
// add theirs whenever they were not the deciding check; the stored
// observation adds its capture-time reason beside a refused reuse when
// its text differs from the judgment's.
func posture(f Finding, inspection FindingInspection, judgeErr error) RecordPosture {
	p := RecordPosture{Symbol: f.Symbol, Measurement: "measured"}
	if f.Cached {
		p.Measurement = "cached"
	}
	switch {
	case judgeErr != nil:
		p.Reuse = FindingUnverifiable
		p.Reasons = append(p.Reasons, PostureReason{Channel: PostureFreshness, Reason: "judgment unavailable: " + judgeErr.Error()})
		p.Analysis = AnalysisJudge
	case inspection.State == FindingCurrent:
		p.Reuse = FindingCurrent
	default:
		p.Reuse = inspection.State
		channel := inspection.Channel
		if channel == "" {
			channel = PostureFreshness
		}
		p.Reasons = append(p.Reasons, PostureReason{Channel: channel, Reason: inspection.Reason})
		if channel != PostureCandidateEvidence && len(inspection.CandidateEvidence) != 0 {
			p.Reasons = append(p.Reasons, PostureReason{Channel: PostureCandidateEvidence, Reason: candidateEvidenceReason(inspection.CandidateEvidence)})
		}
		switch {
		case inspection.State == FindingStale || inspection.State == FindingDetached:
			p.Analysis = AnalysisRemeasure
		case channel == PostureCandidateEvidence || channel == PostureRuntimeInputs:
			p.Analysis = AnalysisReexecute
		default:
			p.Analysis = AnalysisSource
		}
	}
	// The capture-time observation is a refusing channel only beside a
	// refused reuse — a current record may carry one its completed
	// observation evidence served past — and only when its text is not
	// the judgment's own.
	if p.Reuse != FindingCurrent && !f.TargetEvidence.ObservationProof.Observable && f.TargetEvidence.ObservationProof.Reason != "" &&
		f.TargetEvidence.ObservationProof.Reason != strings.TrimPrefix(inspection.Reason, targetReasonPrefix) {
		p.Reasons = append(p.Reasons, PostureReason{Channel: PostureStoredObservation, Reason: f.TargetEvidence.ObservationProof.Reason})
	}
	return p
}

// PropertyOracleNote states one oracle package's property-runtime
// prerequisite (REQ-exec-property-oracles): a pinned runtime names what
// the run pinned itself; an unpinnable one names what the caller must
// ensure for reproducible verdicts.
type PropertyOracleNote struct {
	Package string `json:"package"`
	Runtime string `json:"runtime"`
	Note    string `json:"note"`
}

// propertyOracleFlags is the oracle invocation's property-runtime
// pinning: a rapid package runs with its reproducer files suppressed
// and its draws pinned, so every mutant faces the same draw sequence
// and a verdict is reproducible (REQ-exec-property-oracles).
func propertyOracleFlags(rapid bool) []string {
	if !rapid {
		return nil
	}
	return engine.PropertyOracleBinFlags()
}

// propertyOracleNote renders one package's prerequisite statement.
func propertyOracleNote(pkg, runtime string) (PropertyOracleNote, bool) {
	switch runtime {
	case "rapid":
		return PropertyOracleNote{Package: pkg, Runtime: runtime,
			Note: "draws pinned (-rapid.seed=1) and reproducer files suppressed (-rapid.nofailfile) - verdicts reproducible"}, true
	case "gopter":
		return PropertyOracleNote{Package: pkg, Runtime: runtime,
			Note: "gomutant cannot pin gopter's draws - ensure the suite fixes its seed, or verdicts are unreproducible"}, true
	}
	return PropertyOracleNote{}, false
}

// RunSummary is the aggregate final disposition of one selected target set.
type RunSummary struct {
	// Run is the run's identity (Options.RunID), the value every record
	// this run measured carries; a face sets it beside the totals.
	Run       string `json:"run,omitempty"`
	Targets   int    `json:"targets"`
	Measured  int    `json:"measured"`
	Cached    int    `json:"cached"`
	Skipped   int    `json:"skipped"`
	Generated int    `json:"generated"`
	Discarded int    `json:"discarded"`
	Killed    int    `json:"killed"`
	Survived  int    `json:"survived"`
	Attested  int    `json:"attested"`
	Open      int    `json:"open"`
	// DarkPackages names packages whose ENTIRE selected target set
	// skipped - the blast radius a scattered skip count hides: a dark
	// package carries zero campaign evidence and reads as a coverage
	// hole, not a tool hiccup (REQ-result-skip-radius).
	DarkPackages []string `json:"darkPackages,omitempty"`
	// Selection is the run's declared build selection (SelectionKey);
	// Unreached lists the targets that selection's leg discovered but
	// no oracle reaches — the stated coverage bound on the measured
	// population, never a silent zero (REQ-result-unreached-bound).
	// Absent under no declared selection.
	Selection string   `json:"selection,omitempty"`
	Unreached []string `json:"unreached,omitempty"`
	// Delta is a changed-ref run's cut of the open survivors by the
	// delta's added lines (DeltaSummary); absent on every other run.
	Delta *DeltaSummary `json:"delta,omitempty"`
	// Reusable counts the completed records reusable as they stand;
	// NotReusable lists the others with their posture, capped at
	// PostureCap with the remainder in OmittedNotReusable — the counts
	// above never stand for reuse (REQ-result-run-posture).
	Reusable           int             `json:"reusable"`
	NotReusable        []RecordPosture `json:"notReusable,omitempty"`
	OmittedNotReusable int             `json:"omittedNotReusable,omitempty"`
	// Audit preserves the measured rate of historical narrowed-survivor
	// audit reports. Complete-oracle execution performs no such audit.
	Audit *AuditSummary `json:"audit,omitempty"`
	// Banked is a cancelled run's banked state: what the two layers
	// kept under the named exit cause (REQ-exec-banked-summary);
	// absent on a completed run.
	Banked *BankedState `json:"banked,omitempty"`
}

// PostureCap bounds the not-reusable roster a summary carries.
const PostureCap = 20

// AddPostures folds the run's record postures, keyed by symbol as the
// faces collect them, into the summary: the reusable count and the
// capped roster of the rest, in symbol order.
func (s *RunSummary) AddPostures(postures map[string]RecordPosture) {
	sorted := make([]RecordPosture, 0, len(postures))
	for _, p := range postures {
		sorted = append(sorted, p)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Symbol < sorted[j].Symbol })
	for _, p := range sorted {
		switch {
		case p.Measurement == "skipped":
		case p.Reuse == FindingCurrent:
			s.Reusable++
		case len(s.NotReusable) < PostureCap:
			s.NotReusable = append(s.NotReusable, p)
		default:
			s.OmittedNotReusable++
		}
	}
}

// SummarizeRun derives deterministic aggregate totals from findings.
// packageOf names each finding's package for the dark-package radius
// (REQ-result-skip-radius): a loaded tree's PackageOf, or nil where no
// tree is loaded, which groups by the string cut's guess.
func SummarizeRun(findings []Finding, sel Selection, packageOf func(string) string) RunSummary {
	summary := RunSummary{Targets: len(findings), Selection: SelectionKey(sel)}
	if bound := CoverageBoundOf(findings, sel, ""); bound != nil {
		summary.Unreached = bound.Unreached
	}
	for _, finding := range findings {
		switch {
		case finding.Skipped != "":
			summary.Skipped++
		case finding.Cached:
			summary.Cached++
		default:
			summary.Measured++
		}
		summary.Generated += finding.Generated
		summary.Discarded += finding.Discarded
		summary.Killed += finding.Killed
		summary.Survived += finding.Mutants - finding.Killed
		summary.Attested += len(finding.Attested)
		summary.Open += len(finding.Open())
	}
	for _, radius := range SkippedPackageRadius(findings, packageOf) {
		if radius.Dark() && radius.Targets > 1 {
			summary.DarkPackages = append(summary.DarkPackages, radius.Package)
		}
	}
	return summary
}

// Run mutates each target and executes its oracle per mutant, fanning
// mutant runs across a worker pool (REQ-exec-oracle-run). Prior findings
// are served only when every target and oracle evidence record, operator,
// oracle-timeout and budget pins hold, unless forced (REQ-result-stale). A run that
// cannot attribute an outcome aborts without findings
// (REQ-core-attributed-kills).
func (t *Tree) Run(ctx context.Context, targets []Target, caller Options) ([]Finding, error) {
	trace.Refresh(os.Getenv(trace.EnvTrace), os.Getenv(trace.EnvTraceFile))
	t0 := time.Now()
	trace.Emit(ctx, trace.DomainPrepare, "run_start", t.dir,
		trace.Int("targets", len(targets)),
		trace.Int("prior", len(caller.Prior)),
		trace.Bool("plan_only", caller.PlanOnly),
	)
	t.ensureExplicitTestdataTargets(ctx, targets)
	// The run counts itself: the tallies are installed on the caller's
	// callbacks and delivered on every exit once measurement began, and
	// on every completed or drifted run.
	var tally RunTallies
	tally.Selected = len(targets)
	started := false
	caller = tallyCallbacks(caller, &tally, &started)
	findings, err := t.runCounted(ctx, targets, caller)
	var drift *TreeDriftError
	if caller.Tallies != nil && (started || err == nil || errors.As(err, &drift)) {
		caller.Tallies(tally)
	}
	trace.Emit(ctx, trace.DomainReconcile, "run_done", t.dir,
		trace.Int("findings", len(findings)),
		trace.Int("cached", tally.Served),
		trace.Int("selected", tally.Selected),
		trace.Int64("duration_ms", time.Since(t0).Milliseconds()),
		trace.Err(err),
	)
	return findings, err
}

type resolvedTarget struct {
	index  int
	oracle []string
	// oracleNote is the derivation's self-description for the
	// measure decision: breadth, and any stood-down packages
	// (REQ-target-default's stand-down naming).
	oracleNote string
	// attested carries this target's package-process pairing: its
	// oracle packages all equal its own, so the processes that
	// attribute its subjects' verdicts are its own package's test
	// binaries (gofresh WithPackageProcessExecution's honesty
	// condition, held per target — a cross-package sibling never
	// drops it for the rest of the run).
	attested bool
	// shaped carries a shaped target's pre-resolved candidates;
	// nil for symbol targets (REQ-target-structural,
	// REQ-target-manual-recipes).
	shaped       []engine.Candidate
	shapedDigest string
	// shapedFiles are the shape's provenance inputs, derived once
	// beside the candidates and threaded to every stamp of this
	// target's findings (REQ-result-layers).
	shapedFiles []string
}

type modeViews struct {
	engines    *subjectEngines
	symbols    []string
	views      *subjectViewSet
	viewFaults map[string]error
}

type groupBaseline struct {
	raw           time.Duration
	budget        time.Duration
	passed        []string
	testDurations map[string]time.Duration
	coverage      *engine.Coverage
	coverPkgs     []string
}

type executedWindow struct {
	window       []work
	observations [][]gofresh.Observation
	incompletes  [][]string
	// scores carries each target's verdict-bearing measurement —
	// outcomes, killers, and the serial-confirmation flips — as one
	// value, so no assembly path can receive the verdicts without
	// the flips (REQ-exec-survivor-evidence).
	scores []windowScores
	// discard is non-nil exactly when Options.SoftStop drained the
	// window: the fresh works were truncated to their dispatched
	// candidate prefixes and restamped as candidate-capped, each
	// true entry marks a work with nothing committable, and the run
	// ends after this window aggregates (REQ-exec-cancellation's
	// graceful-interrupt clause).
	discard []bool
}

type campaignRunner struct {
	t                    *Tree
	targets              []Target
	opts                 runOptions
	deriveOracleBudgets  bool
	runStart             time.Time
	repository           repositoryState
	residue              func() string
	driftedMu            sync.Mutex
	drifted              []TargetDrift
	preparation          *runPreparation
	jobs                 int
	runEnv               []string
	oracleMemoryPin      int64
	selKey               string
	prior                map[string]*Finding
	crossSelectionPrior  []Finding
	findings             []Finding
	resolvedTargets      []resolvedTarget
	baselineCache        map[baselineKey]gofresh.Observation
	scopedBaselines      map[scopedBaselineKey][]string
	coverageCache        map[string]engine.Coverage
	bankScope            string
	guidanceCache        map[string]oracleAttribution
	decisions            []RunDecision
	modes                map[bool]*modeViews
	probeGate            sync.RWMutex
	unitOf               []*proofUnit
	proofUnits           map[string]*proofUnit
	unitMu               sync.Mutex
	unitDone             []bool
	oraclePackages       []string
	coverPkgsByOraclePkg map[string][]string
	preflightBracket     func() error
	preparedTargets      atomic.Int64
	preparedCandidates   atomic.Int64
	baselineFailures     map[baselineKey]string
	groupBudgetMu        sync.Mutex
	groupBaselines       map[baselineKey]groupBaseline
	pendingBankDeposits  map[baselineKey]pendingBankDeposit
	interner             *manifestInterner
	committed            []bool
	dispatchedTargets    int
	mutantsDone          atomic.Int64
	tickMu               sync.Mutex
}

func (t *Tree) runCounted(ctx context.Context, targets []Target, caller Options) ([]Finding, error) {
	// The run's view of its options: the caller's surface beside the
	// services this function installs — the one constructor of
	// runOptions.
	opts := runOptions{Options: caller}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.RunID == "" {
		opts.RunID = NewRunID()
	} else if err := validateRunID(opts.RunID); err != nil {
		return nil, err
	}
	if err := validateRunBounds(opts.Budget, opts.OracleTimeout, opts.AnalysisBudget); err != nil {
		return nil, err
	}
	if opts.PlanOnly {
		// The plan clause's no-write guarantee is the run's own: even a
		// cached serve's incremental commit is suppressed here, so no
		// caller has to re-implement the suppression.
		opts.Commit = nil
	}
	if err := validateBracketPaths(t.dir, opts.BracketPaths); err != nil {
		return nil, err
	}
	for _, namespace := range opts.ScratchNamespaces {
		if err := gofresh.ValidateScratchNamespace(namespace.Dir, namespace.Pattern); err != nil {
			return nil, fmt.Errorf("gomutant: scratch namespace %s:%s refused before measurement: %w", namespace.Dir, namespace.Pattern, err)
		}
	}
	// The oracle budget: an explicit timeout is the caller's override;
	// 0 derives each group's budget from its own measured baseline —
	// the baseline probe IS a measurement of the oracle's cost on this
	// tree under this load, the same derivation the ephemeral face
	// carries (REQ-exec-oracle-run's derived campaign budget). In
	// derive mode the run-wide bound becomes the campaign measurement
	// leash: it governs baselines and advisory probes, while every
	// verdict-bearing process runs under its group's derived budget.
	deriveOracleBudgets := opts.OracleTimeout == 0
	if deriveOracleBudgets {
		opts.OracleTimeout = campaignBaselineLeash
	}
	opts.Options = lockCallbacks(opts.Options)
	targets = snapshotTargets(targets)
	opts.Prior = snapshotFindings(opts.Prior)
	runStart := time.Now()
	repository, err := captureRepositoryStateContext(ctx, t.dir, opts.Staged)
	if err != nil {
		return nil, err
	}
	// residue evaluates the untracked-file listing once for the whole
	// run: a wide drift refuses many targets against the same tree
	// state, and the naming needs the residue set, not a per-target
	// re-listing.
	// The store's own paths join the caller's declared writes in a copy:
	// opts.OwnWrites itself stays the caller's declaration — its presence
	// marks a findings-producing run for the baseline bank below.
	residue := sync.OnceValue(func() string {
		return measurementResidue(ctx, repository, runStart, append(append([]string(nil), opts.OwnWrites...), StoreOwnPaths(t.dir)...))
	})

	preparation := newRunPreparation(t)
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	// The run's oracle bounds — the memory ceiling and the
	// inner-parallelism width — are ONE value derived here and handed
	// to every oracle spawn, ingest mirror, merge, and splice below:
	// never process state, so a concurrent probe or campaign in the
	// same process cannot move them under this run
	// (REQ-exec-oracle-memory, REQ-exec-oracle-parallelism). The width
	// is a scheduling bound, deliberately unpinned: it reaches verdicts
	// through the wall-clock oracle timeout like ambient load, and
	// through the recorded environment evidence exactly where an oracle
	// observably reads it.
	opts.bounds = engine.DeriveOracleBounds(opts.OracleMemoryBytes, jobs)
	defer opts.bounds.Close()
	// The campaign's one evidence environment, mode-independent (env plus
	// the run's width; the per-mode engine sets are built after
	// resolution, when each target's attestation is known).
	runEnv := engine.OracleEvidenceEnv(t.eng.GoEnv(), opts.bounds)
	// The pin the run's evidence records and compares.
	oracleMemoryPin := opts.bounds.MemoryBytes
	selKey := SelectionKey(t.selection)
	// First match for this run's selection wins; duplicate symbols occur
	// only in hand-edited documents.
	prior := map[string]*Finding{}
	var crossSelectionPrior []Finding
	for i := range opts.Prior {
		f := &opts.Prior[i]
		if f.Selection != selKey {
			crossSelectionPrior = append(crossSelectionPrior, *f)
			continue
		}
		if _, ok := prior[f.Symbol]; !ok {
			prior[f.Symbol] = f
		}
	}
	// Findings are keyed by symbol (REQ-result-record): two targets naming
	// one symbol would collide in the document, so the set is refused up
	// front rather than one silently shadowing the other.
	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg.Symbol] {
			return nil, fmt.Errorf("gomutant: duplicate target symbol %s", tg.Symbol)
		}
		seen[tg.Symbol] = true
	}
	// The bank is WRITTEN by findings-producing runs only (OwnWrites is
	// their marker, and the campaign lock they hold serializes the
	// file); library embeddings and probes deposit nothing — the
	// ephemeral face reads it for its leash lift, a lock-free read the
	// atomic rename keeps whole. Deposits persist
	// as they land — an exit-time save dies with a killed process —
	// and the deferred save is only the flush backstop
	// (REQ-result-baseline-bank).
	if len(opts.OwnWrites) > 0 {
		opts.baselineBank = openBaselineBank(t.dir)
		defer opts.baselineBank.save()
	}

	r := &campaignRunner{
		t:                    t,
		targets:              targets,
		opts:                 opts,
		deriveOracleBudgets:  deriveOracleBudgets,
		runStart:             runStart,
		repository:           repository,
		residue:              residue,
		preparation:          preparation,
		jobs:                 jobs,
		runEnv:               runEnv,
		oracleMemoryPin:      oracleMemoryPin,
		selKey:               selKey,
		prior:                prior,
		crossSelectionPrior:  crossSelectionPrior,
		findings:             make([]Finding, len(targets)),
		baselineCache:        map[baselineKey]gofresh.Observation{},
		scopedBaselines:      map[scopedBaselineKey][]string{},
		coverageCache:        map[string]engine.Coverage{},
		bankScope:            bankScopeString(opts.BracketPaths, opts.ScratchNamespaces),
		guidanceCache:        map[string]oracleAttribution{},
		decisions:            make([]RunDecision, len(targets)),
		modes:                map[bool]*modeViews{},
		proofUnits:           map[string]*proofUnit{},
		coverPkgsByOraclePkg: map[string][]string{},
		baselineFailures:     map[baselineKey]string{},
		groupBaselines:       map[baselineKey]groupBaseline{},
		pendingBankDeposits:  map[baselineKey]pendingBankDeposit{},
		interner:             &manifestInterner{byDigest: map[string]string{}},
		committed:            make([]bool, len(targets)),
	}
	if err := r.resolveTargetsAndViews(ctx); err != nil {
		return nil, err
	}
	r.setupGroupBudgetServices()
	if r.opts.PlanOnly {
		return r.runPlanOnly(ctx)
	}
	return r.runExecutionPipeline(ctx)
}

func (r *campaignRunner) targetDone(i int) {
	if r.unitOf == nil {
		return
	}
	r.unitMu.Lock()
	defer r.unitMu.Unlock()
	if r.unitDone[i] {
		return
	}
	r.unitDone[i] = true
	if u := r.unitOf[i]; u != nil {
		u.remaining--
		if u.remaining == 0 {
			u.release()
		}
	}
}

func (r *campaignRunner) refuseTarget(i int, reason string) {
	r.driftedMu.Lock()
	r.drifted = append(r.drifted, TargetDrift{Symbol: r.targets[i].Symbol, Reason: reason})
	r.driftedMu.Unlock()
	r.targetDone(i)
}

func (r *campaignRunner) skipTarget(ctx context.Context, i int, reason string, emit bool) {
	r.findings[i].Skipped = reason
	r.decisions[i] = RunDecision{Symbol: r.targets[i].Symbol, Action: "skipped", Reason: reason}
	trace.Emit(ctx, trace.DomainReconcile, "target_decision", r.targets[i].Symbol,
		trace.String("action", "skipped"),
		trace.String("reason", reason),
	)
	if emit && r.opts.Decision != nil {
		r.opts.Decision(r.decisions[i])
	}
	r.targetDone(i)
}

func (r *campaignRunner) refuseSkipped(ctx context.Context, i int, reason string, emit bool) {
	full := reason + r.residue()
	r.refuseTarget(i, full)
	r.skipTarget(ctx, i, full, emit)
}

func (r *campaignRunner) cacheTarget(ctx context.Context, i int, reason string) {
	r.decisions[i] = RunDecision{Symbol: r.targets[i].Symbol, Action: "cached", Reason: reason}
	trace.Emit(ctx, trace.DomainReconcile, "target_decision", r.targets[i].Symbol,
		trace.String("action", "cached"),
		trace.String("reason", reason),
	)
	if r.opts.Decision != nil {
		r.opts.Decision(r.decisions[i])
	}
	r.targetDone(i)
}

func (r *campaignRunner) annotateCrossSelection(f Finding) Finding {
	if len(r.crossSelectionPrior) == 0 || f.Skipped != "" {
		return f
	}
	bundle := make([]Finding, 0, 1+len(r.crossSelectionPrior))
	bundle = append(bundle, f)
	for _, other := range r.crossSelectionPrior {
		if other.Symbol == f.Symbol {
			bundle = append(bundle, other)
		}
	}
	if len(bundle) < 2 {
		return f
	}
	return AnnotateSelectionVerdicts(bundle)[0]
}

func (r *campaignRunner) eachMode(fn func(mv *modeViews) error) error {
	for _, attested := range []bool{false, true} {
		if mv, ok := r.modes[attested]; ok {
			if err := fn(mv); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *campaignRunner) modeFor(attested bool) *modeViews {
	mv, ok := r.modes[attested]
	if !ok {
		mv = &modeViews{
			engines:    r.t.newSubjectEngines(r.opts.AnalysisEvent, attested, r.opts.bounds, r.opts.AnalysisBudget),
			views:      &subjectViewSet{bySymbol: map[string]*subjectView{}, bounds: r.opts.bounds, packageProcess: attested},
			viewFaults: map[string]error{},
		}
		r.modes[attested] = mv
	}
	return mv
}

// ErrInterrupted is the graceful-interrupt run result: an operational
// cancellation — the campaign did not finish — whose drained prefixes
// are already committed as candidate-capped records; re-running the
// same command serves the committed work and measures the remainder.
// Exported so faces and embedders can classify the one outcome the
// graceful drain exists to distinguish from a hard abort
// (REQ-exec-cancellation's graceful-interrupt clause).
var ErrInterrupted = errors.New("gomutant: interrupted - in-flight mutants drained and measured prefixes committed as candidate-capped records; re-run the same command to measure the remainder")
