package gomutant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	iofs "io/fs"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/windowcost"
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
	// measured baseline (a multiple with a 60s floor), the finding
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
	ScratchNamespaces []runtimeinput.ScratchNamespace
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
	return findings, err
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
		if err := runtimeinput.ValidateScratchNamespace(namespace.Dir, namespace.Pattern); err != nil {
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
	// driftedMu guards drifted: the preparation goroutine's cached-serve
	// refusal and the aggregation loop's refusals append concurrently
	// (the pipeline runs preparation ahead of execution).
	var driftedMu sync.Mutex
	var drifted []TargetDrift
	// targetDone records a target's terminal disposition for its proof
	// unit — committed, cached, skipped, refused or discarded — once per
	// target; the unit releases at its last. Assigned once the units
	// exist; a terminal reached before then belongs to no unit.
	targetDone := func(int) {}
	refuseTarget := func(i int, reason string) {
		driftedMu.Lock()
		drifted = append(drifted, TargetDrift{Symbol: targets[i].Symbol, Reason: reason})
		driftedMu.Unlock()
		targetDone(i)
	}

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
	annotateCrossSelection := func(f Finding) Finding {
		if len(crossSelectionPrior) == 0 || f.Skipped != "" {
			return f
		}
		bundle := make([]Finding, 0, 1+len(crossSelectionPrior))
		bundle = append(bundle, f)
		for _, other := range crossSelectionPrior {
			if other.Symbol == f.Symbol {
				bundle = append(bundle, other)
			}
		}
		if len(bundle) < 2 {
			return f
		}
		return AnnotateSelectionVerdicts(bundle)[0]
	}

	// Phase one, sequential: resolve every target to a terminal finding
	// (skipped, cached) or to a mutant work list.
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

	findings := make([]Finding, len(targets))
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
	var resolvedTargets []resolvedTarget
	// keyFor is the one group→key projection: every map keyed by
	// baselineKey must agree on it exactly.
	keyFor := baselineKeyFor
	baselineCache := map[baselineKey]runtimeinput.Observation{}
	// scopedBaselines memoizes killer-scoped baseline probes for the
	// serial confirmation's differential ground — Run-local like
	// baselineCache, which is what makes the key complete (bounds,
	// brackets, namespaces, and the injected env are per-Run pins)
	// and the lock-free access true (this Run's probe gate serializes
	// every confirmation; a Tree-scoped map would race across the
	// MCP server's concurrent campaigns over one cached Tree).
	scopedBaselines := map[scopedBaselineKey][]string{}
	coverageCache := map[string]engine.Coverage{}
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
	// bankScope folds the observation-shaping per-run axes into the
	// persisted bank key: bracket paths and scratch namespaces change
	// what a probe records, so entries never serve across differing
	// declarations. The remaining per-run axes are deliberately
	// unpinned — budget bounds never shape the observation (and a
	// stale duration is evidence-gated), and the environment is
	// re-verified at serve time by the evidence memo and adoption.
	bankScope := bankScopeString(opts.BracketPaths, opts.ScratchNamespaces)
	guidanceCache := map[string]oracleAttribution{}
	decisions := make([]RunDecision, len(targets))
	// skipTarget is the one no-measurement exit: the finding's mark and
	// the decision row (vocabulary "skipped" — the decision stream's
	// only no-measurement action, REQ-exec-run-status). emit=true for
	// exits past deliverPrepared's ordered walk, which must stream their
	// own row; resolve-phase exits leave emission to the walk. The
	// hand-picked per-site subsets this replaces bred out-of-order and
	// missing-row defects; every new exit goes through here.
	skipTarget := func(i int, reason string, emit bool) {
		findings[i].Skipped = reason
		decisions[i] = RunDecision{Symbol: targets[i].Symbol, Action: "skipped", Reason: reason}
		if emit && opts.Decision != nil {
			opts.Decision(decisions[i])
		}
		targetDone(i)
	}
	// refuseSkipped is a preparation-phase drift refusal: the refused
	// registry entry (the run fails operationally with the refused set,
	// REQ-exec-quiescence) plus the same skipped decision every
	// no-measurement target reports, both carrying the run's residue
	// provenance — the decision line names it per REQ-exec-quiescence.
	// Execution-phase refusals call refuseTarget alone: their measure
	// decision already streamed, and a second row per target would break
	// the once-per-target decision discipline.
	refuseSkipped := func(i int, reason string, emit bool) {
		full := reason + residue()
		refuseTarget(i, full)
		skipTarget(i, full, emit)
	}
	// cacheTarget records a wholesale cached serve's decision, always
	// emitted inline: every serve runs past deliverPrepared's walk. (A
	// cached serve that re-executes flagged candidates flows through
	// the shared measure-decision path instead, carrying its count.)
	cacheTarget := func(i int, reason string) {
		decisions[i] = RunDecision{Symbol: targets[i].Symbol, Action: "cached", Reason: reason}
		if opts.Decision != nil {
			opts.Decision(decisions[i])
		}
		targetDone(i)
	}
	for i, tg := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationResolving, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Selection: selKey, Labels: tg.Labels, OperatorSet: engine.OperatorSet, OracleExplicit: tg.explicitOracle(), OracleTimeout: opts.OracleTimeout.String(), OracleMemoryBytes: oracleMemoryPin}
		if deriveOracleBudgets {
			// The derived record's bound starts at the floor and raises
			// to the loosest budget any verdict runs under
			// (mergeScoredFacts); the leash is a measurement ceiling,
			// never a recorded bound.
			f.OracleTimeout = seams.ephemeralBudgetFloor.String()
			f.OracleTimeoutDerived = true
		}
		if tg.Shaped() {
			// Validation precedes oracle derivation: a shaped identity
			// resolves to no package, so deriving an oracle from it
			// would be a run fault where the contract wants a
			// target-local refusal (an explicit oracle is required).
			if err := validateShapedTarget(tg); err != nil {
				skipTarget(i, "shaped target refused: "+err.Error(), false)
				continue
			}
		}
		oracle, oracleStoodDown, oracleDerived, err := preparation.oracle(ctx, tg)
		if err != nil {
			return nil, err
		}
		// The derivation names its own caps and breadth: the measure
		// decision carries how wide the derived oracle is and which
		// test-bearing packages the derivation could not consult — a
		// silently narrowed oracle is the defect class this derivation
		// exists to close (REQ-target-default's stand-down naming).
		oracleNote := ""
		if len(tg.Oracle) == 0 && !tg.OracleExplicit {
			if len(oracle) > 0 {
				oracleNote = fmt.Sprintf("; oracle: %s across %s", testNoun(len(oracle)), packageNoun(len(pkgRuns(oracle))))
			}
			if len(oracleStoodDown) > 0 {
				oracleNote += "; derivation stood down on: " + strings.Join(oracleStoodDown, ", ")
			}
		}
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default). An oracle that exists but could not
			// be consulted is NOT "no oracle" — the stood-down packages
			// and the escape ride the skip, the naming duty's worst
			// case (a dark symbol filed as unwitnessed when a consumer
			// suite merely failed to resolve).
			reason := "no oracle"
			if len(oracleStoodDown) > 0 {
				reason += " — derivation stood down on: " + strings.Join(oracleStoodDown, ", ") + "; resolve the closure, or pass an explicit oracle"
			}
			skipTarget(i, reason, false)
			// A derivation that ran over the target's resolved package
			// and found no test with nothing stood down is the
			// selection's coverage bound: no test of the declared leg
			// reaches the target. An explicit empty statement and an
			// unresolved symbol never are (REQ-result-unreached-bound).
			findings[i].Unreached = oracleDerived && len(oracleStoodDown) == 0
			continue
		}
		if err := preparation.validateOracle(ctx, oracle); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Target-local: one target's broken oracle set never takes
			// sibling targets down (REQ-exec-quiescence's locality,
			// which governs resolution and decision evidence exactly as
			// it governs freshness-proof construction).
			skipTarget(i, "oracle validation failed: "+err.Error(), false)
			continue
		}
		if tg.Shaped() {
			// A shaped target resolves from its declared parameters:
			// no body, no catalog, its own pin (REQ-target-structural,
			// REQ-target-manual-recipes). Refusals are target-local
			// skips exactly as resolution failures are.
			candidates, digest, shapedFiles, err := t.shapedCandidates(ctx, tg)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				// A drift-shaped cause (a recipe or probe file moved or
				// vanished since load) refuses into the operational-
				// failure set exactly as the phase-one paths do — the
				// amended contract holds universally, shaped targets
				// included (REQ-exec-quiescence).
				var sourceDrift *engine.SourceDriftError
				if errors.As(err, &sourceDrift) || errors.Is(err, iofs.ErrNotExist) {
					refuseSkipped(i, "shaped source drifted since load: "+err.Error()+" - re-run when the tree settles", false)
					continue
				}
				skipTarget(i, "shaped resolution failed: "+err.Error(), false)
				continue
			}
			f.BodyHash = digest
			f.OperatorSet = shapedOperatorSet
			f.Shape = &TargetShape{Structural: tg.Structural, Manual: tg.Manual}
			resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle, oracleNote: oracleNote, shaped: candidates, shapedDigest: digest, shapedFiles: shapedFiles, attested: packageProcessAttestable(t.PackageOf, tg.Symbol, oracle)})
			continue
		}
		bodyHash, err := t.eng.BodyHashContext(ctx, tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			skipTarget(i, "not a function - for mutation adequacy, target its methods or the bound function-level subjects", false)
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// A resolution failure caused by a loaded file VANISHING is
			// drift exactly like a modified one (a checkout mid-run) —
			// refused into the operational-failure set, never a quiet
			// skip a pipeline reads as success. Every other typed-load
			// breakage stays this target's own skip with the cause
			// (REQ-exec-quiescence).
			if errors.Is(err, iofs.ErrNotExist) {
				refuseSkipped(i, "source vanished since load: "+err.Error()+" - re-run when the tree settles", false)
				continue
			}
			skipTarget(i, "target resolution failed: "+err.Error(), false)
			continue
		}
		f.BodyHash = bodyHash
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationFreshness, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle, oracleNote: oracleNote, attested: packageProcessAttestable(t.PackageOf, tg.Symbol, oracle)})
	}
	// Per-mode view bundles: each target's attestation is its own (the
	// pairing above), so a mixed run builds one engine and view set per
	// mode present rather than dropping the attestation run-wide — the
	// coupling that made an unrelated cross-package target re-measure a
	// same-package sibling's verifiable records.
	type modeViews struct {
		engines    *subjectEngines
		symbols    []string
		views      *subjectViewSet
		viewFaults map[string]error
	}
	modes := map[bool]*modeViews{}
	// eachMode walks the modes present in a fixed order (cross-package
	// first): with at most two entries the order carries no semantics,
	// but a map range would make which mode's error text surfaces
	// nondeterministic when both fail.
	eachMode := func(fn func(mv *modeViews) error) error {
		for _, attested := range []bool{false, true} {
			if mv, ok := modes[attested]; ok {
				if err := fn(mv); err != nil {
					return err
				}
			}
		}
		return nil
	}
	modeFor := func(attested bool) *modeViews {
		mv, ok := modes[attested]
		if !ok {
			mv = &modeViews{
				engines:    t.newSubjectEngines(opts.AnalysisEvent, attested, opts.bounds, opts.AnalysisBudget),
				views:      &subjectViewSet{bySymbol: map[string]*subjectView{}, bounds: opts.bounds, packageProcess: attested},
				viewFaults: map[string]error{},
			}
			modes[attested] = mv
		}
		return mv
	}
	for _, resolved := range resolvedTargets {
		mv := modeFor(resolved.attested)
		if resolved.shaped == nil {
			mv.symbols = append(mv.symbols, targets[resolved.index].Symbol)
		}
		mv.symbols = append(mv.symbols, resolved.oracle...)
	}
	if err := eachMode(func(mv *modeViews) error {
		if len(mv.symbols) == 0 {
			return nil
		}
		reportPreparation(opts.Progress, pricedPass(PreparationViews, mv.symbols, t.PackageOf))
		var err error
		mv.views, mv.viewFaults, err = t.buildSubjectViews(ctx, mv.symbols, preparation.packageContext, mv.engines)
		if err != nil {
			return fmt.Errorf("freshness: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// Decision-evidence faults route target-locally exactly as the
	// observed union's do (REQ-exec-quiescence): a target whose own
	// symbol or any of whose oracle symbols has no decision view skips
	// with the cause, and its siblings proceed.
	{
		kept := resolvedTargets[:0]
		for _, rt := range resolvedTargets {
			mv := modeFor(rt.attested)
			var cause error
			needed := rt.oracle
			if rt.shaped == nil {
				needed = append([]string{targets[rt.index].Symbol}, rt.oracle...)
			}
			for _, symbol := range needed {
				if fault, ok := mv.viewFaults[symbol]; ok {
					cause = fault
					break
				}
				if mv.views.bySymbol[symbol] == nil {
					cause = fmt.Errorf("no decision view for %s", symbol)
					break
				}
			}
			if cause == nil {
				kept = append(kept, rt)
				continue
			}
			skipTarget(rt.index, "decision evidence unavailable: "+cause.Error(), false)
		}
		resolvedTargets = kept
	}
	// probeGate serializes producer-side oracle probes against serial kill
	// confirmations: a confirmation's scored run shares no process with any
	// preparation probe, so a probe's test-level side effects (an exclusive
	// port, a file lock) can never manufacture a false reproduction or a
	// false flip. Probes hold it shared among themselves; each confirmation
	// holds it exclusively (REQ-exec-attribution).
	var probeGate sync.RWMutex
	opts.probeGate = &probeGate
	if seams.probeGateInstalled != nil {
		seams.probeGateInstalled(&probeGate)
	}
	// The proof units — the targets of one mode sharing an oracle
	// package set — are the unit loop's vertical for the derived
	// oracle: a unit's union proves its targets' own symbols and their
	// oracles together, as siblings of the mode's decision views (one
	// observation per module group serves the decision and the proof
	// roles; per-subject evidence is identical by gofresh's
	// batch-equivalence contract; the per-target build survives only as
	// the bounded retry, REQ-exec-quiescence), when its first target
	// reaches the proof, and releases its union and faults when its
	// last target reaches a terminal disposition. A fully-cached warm
	// run — every target served — pays no pass at all. Preparation is
	// serial, so no two units prove at once, and it runs ahead of
	// execution without bound (the window gatherer blocks for its
	// budget: the execution window is a pure function of the tree, the
	// target order and the worker count, REQ-exec-oracle-run), so a
	// window's commits wait for the next window's preparation — a later
	// unit's pass included — and the proofs of every prepared, not yet
	// committed unit are held until their units release
	// (REQ-exec-analysis-budget). The pass runs its observed captures
	// (test processes) under the probe gate held shared, exactly like
	// the per-target retry builds: a later target prepares while
	// earlier targets' windows execute, so a serial confirmation can be
	// in flight — the gate, not any ordering premise, is what keeps the
	// pass's processes out of a confirmation's isolation window. Built
	// over the targets a decision fault left standing: a skipped target
	// belongs to no unit.
	unitOf := make([]*proofUnit, len(targets))
	proofUnits := map[string]*proofUnit{}
	for _, resolved := range resolvedTargets {
		var pkgs []string
		for _, run := range pkgRuns(resolved.oracle) {
			pkgs = append(pkgs, run.pkg)
		}
		key := fmt.Sprintf("%t\x00%s", resolved.attested, strings.Join(pkgs, "\x00"))
		u, ok := proofUnits[key]
		if !ok {
			mv := modes[resolved.attested]
			u = &proofUnit{key: key, packages: pkgs, decision: mv.views, engines: mv.engines}
			proofUnits[key] = u
		}
		if resolved.shaped == nil {
			u.symbols = append(u.symbols, targets[resolved.index].Symbol)
		}
		u.symbols = append(u.symbols, resolved.oracle...)
		u.remaining++
		unitOf[resolved.index] = u
	}
	for _, u := range proofUnits {
		sort.Strings(u.symbols)
		u.symbols = slices.Compact(u.symbols)
	}
	var unitMu sync.Mutex
	unitDone := make([]bool, len(targets))
	targetDone = func(i int) {
		unitMu.Lock()
		defer unitMu.Unlock()
		if unitDone[i] {
			return
		}
		unitDone[i] = true
		if u := unitOf[i]; u != nil {
			u.remaining--
			if u.remaining == 0 {
				u.release()
			}
		}
	}
	buildProofUnit := func(u *proofUnit) error {
		if u.built {
			return nil
		}
		u.built = true
		if opts.proofAttempt != nil {
			opts.proofAttempt(strings.Join(u.packages, ","), 1)
		}
		// The unit's pass is priced before it is paid: its subjects and
		// the packages they span (REQ-exec-run-status).
		reportPreparation(opts.Progress, pricedPass(PreparationProofs, u.symbols, t.PackageOf))
		probeGate.RLock()
		defer probeGate.RUnlock()
		// Every symbol a standing target needs has a decision view (a
		// decision fault skipped its targets before this point), so the
		// unit's faults are its proof captures' alone.
		union, faults, err := u.decision.observedFor(ctx, u.symbols)
		if err != nil {
			return fmt.Errorf("freshness proofs (unit over %d subjects): %w", len(u.symbols), err)
		}
		u.union, u.faults = union, faults
		return nil
	}
	var oraclePackages []string
	seenOraclePackage := map[string]bool{}
	for _, resolved := range resolvedTargets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, run := range pkgRuns(resolved.oracle) {
			if !seenOraclePackage[run.pkg] {
				seenOraclePackage[run.pkg] = true
				oraclePackages = append(oraclePackages, run.pkg)
			}
		}
	}
	// Bracket-path preflight is once per run at the tree root — the base
	// every spawn's frame resolves against — fired at the first target's
	// measure decision before its observed union (REQ-exec-preparation's
	// loaded-set stage): a run serving every target never captures the
	// preflight bracket (the faces proved the paths exist before the
	// load), a plan reaches it, and nothing costly precedes it.
	preflightBracket := sync.OnceValue(func() error {
		return preflightBracketPaths(ctx, t.dir, opts.BracketPaths)
	})
	preflightOracleModules := func(oracle []string) error {
		if len(opts.BracketPaths) == 0 {
			return nil
		}
		return preflightBracket()
	}
	// producerViewsFor builds a target's observed producer views: its
	// proof unit's union narrowed to the target or, on a fault the
	// campaign has not cancelled, one bounded per-target rebuild — the field
	// failure mode is transient pressure faulting one symbol or module
	// group out of the shared union, gone by the time anyone reads the
	// fault, so a transient union fault costs one extra pass for one
	// target, never a skip (REQ-exec-quiescence); the second attempt is
	// reported to the proof hook. A union that cannot build at all is
	// the campaign's failure (err); a per-target fault is the caller's
	// to route (fault).
	producerViewsFor := func(u *proofUnit, target string, oracle []string, rebuild []string) (views *subjectViewSet, fault, err error) {
		if err := buildProofUnit(u); err != nil {
			return nil, nil, err
		}
		views, fault = u.union.forTarget(target, oracle, u.faults)
		if fault != nil && ctx.Err() == nil {
			if opts.proofAttempt != nil {
				opts.proofAttempt(target, 2)
			}
			probeGate.RLock()
			var rebuilt *observedViewSet
			rebuilt, fault = t.newStrictObservedViews(ctx, rebuild, preparation.packageContext, u.engines)
			if fault == nil {
				views = rebuilt.subjectViewSet
			}
			probeGate.RUnlock()
		}
		return views, fault, nil
	}
	// preparedTargets and preparedCandidates are the pipelined campaign's
	// running tallies: written by the preparation goroutine as each measure
	// target readies, read by the advisory execution events, which grow to
	// the campaign-wide totals as preparation completes
	// (REQ-exec-run-status's advisory classes).
	var preparedTargets, preparedCandidates atomic.Int64
	// prepareTarget runs one target's whole preparation — reuse gates,
	// candidate generation, decision, oracle groups, and baseline probes —
	// and returns its work item, or nil when the target completed without
	// execution (skipped, cached, plan-only). Its decision is delivered
	// after the target's own preparation events, streaming in target order
	// (REQ-exec-run-status); the caller drives it serially, either inline
	// (plan-only) or from the preparation goroutine pipelined with the
	// execution windows.
	// baselineFailures memoizes a package group's failing baseline for
	// the campaign: siblings sharing the flaky package skip on the
	// recorded reason without re-probing. Serial-only (the preparation
	// alone touches it), so it rides under no lock.
	baselineFailures := map[baselineKey]string{}
	// groupBudgetMu guards the two group maps the preparation and the
	// pipelined workers share: groupBaselines (written by the serial
	// preparation as each group first probes, read by the workers —
	// REQ-exec-oracle-run's derived campaign budget) and
	// pendingBankDeposits (written by the preparation, read and deleted
	// on the commit path, which runs concurrently with it).
	var groupBudgetMu sync.Mutex
	// groupBaselines keys each group's ONE baseline measurement: the
	// raw passing wall-clock (the window cost model's whole-group
	// price, recorded in every budget mode — an explicit timeout
	// changes the budget, not what the baseline measured) and the
	// budget derived from it (derive mode only). One entry per probe
	// makes "a group with a budget has a raw duration" structural.
	type groupBaseline struct {
		raw    time.Duration
		budget time.Duration
		passed []string
	}
	groupBaselines := map[baselineKey]groupBaseline{}
	// pendingBankDeposits holds really-probed baselines until their
	// finding's evidence rows complete the deposit (guarded by
	// groupBudgetMu; the scope is stated there).
	pendingBankDeposits := map[baselineKey]pendingBankDeposit{}
	budgetFor := func(g group) time.Duration {
		if !deriveOracleBudgets {
			return opts.OracleTimeout
		}
		groupBudgetMu.Lock()
		defer groupBudgetMu.Unlock()
		if b, ok := groupBaselines[keyFor(g)]; ok && b.budget > 0 {
			return b.budget
		}
		// A group never baselined under this run (a narrowed
		// confirmation scope, a probe): the leash — a generous bound
		// that fabricates nothing.
		return opts.OracleTimeout
	}
	opts.groupBudget = budgetFor
	// The leash a group's advisory probes run under: the campaign leash
	// lifted by the group's own baseline (measured or served from the
	// bank) in derive mode — the same lift the baseline itself gets on
	// the miss path — and the caller's explicit timeout otherwise.
	opts.groupLeash = func(g group) time.Duration {
		if !deriveOracleBudgets {
			return opts.OracleTimeout
		}
		groupBudgetMu.Lock()
		defer groupBudgetMu.Unlock()
		b, ok := groupBaselines[keyFor(g)]
		if !ok {
			return opts.OracleTimeout
		}
		return leashFor(opts.OracleTimeout, b.raw)
	}
	opts.baselineDur = func(g group) (time.Duration, bool) {
		if seams.baselinePrice != nil {
			if d, ok := seams.baselinePrice(g.pkgs); ok {
				return d, true
			}
		}
		groupBudgetMu.Lock()
		defer groupBudgetMu.Unlock()
		b, ok := groupBaselines[keyFor(g)]
		return b.raw, ok
	}
	// probeGroupBaselines fills w.baselines for the target's oracle
	// groups. A non-cancellation baseline condition - a probe failure,
	// an empty match, a failing test - is the target's own condition:
	// returned as a skip reason with the cause named (a flaky test
	// reads as itself, with the failing tests listed, never as a
	// campaign failure), and memoized per package group. A
	// campaign-wide abort remains reserved for cancellation of the run
	// itself (REQ-exec-quiescence's baseline locality).
	probeGroupBaselines := func(ctx context.Context, tg Target, w *work) (string, error) {
		w.baselines = make([]runtimeinput.Observation, 0, len(w.groups))
		for gi := range w.groups {
			group := w.groups[gi]
			key := keyFor(group)
			if reason, failed := baselineFailures[key]; failed {
				return reason, nil
			}
			state, ok := baselineCache[key]
			var banked bankedBaseline
			var hit bool
			if !ok {
				// The bank consult (REQ-result-baseline-bank): a banked
				// group whose pins re-verify serves its measurement —
				// the observation re-enters through adoption, which
				// re-evaluates every recorded identity against disk and
				// refuses on drift; any failure falls through to the
				// probe.
				banked, hit = opts.baselineBank.baseline(key.persistedKey(bankScope))
				if !opts.Force && hit {
					views := groupOracleViews(*w, group)
					// A pin-check ERROR falls through to the probe
					// exactly like a pin mismatch — the bank never
					// aborts a campaign (REQ-result-baseline-bank);
					// only the run's own cancellation propagates.
					held, herr := bankPinsHold(ctx, banked.Evidence, views)
					if herr != nil && ctx.Err() != nil {
						return "", ctx.Err()
					}
					if herr == nil && held && len(banked.PassedTests) > 0 {
						adopted, aerr := runtimeinput.Adopt(banked.Manifest, t.dir, fmt.Sprintf("gomutant-banked-baseline-%d", findingObservationSequence.Add(1)), runEnv)
						if aerr == nil && adopted.Digest == banked.Digest {
							reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0], Banked: true})
							raw := time.Duration(banked.RawMillis) * time.Millisecond
							if deriveOracleBudgets {
								budget := derivedOracleBudget(raw)
								groupBudgetMu.Lock()
								groupBaselines[key] = groupBaseline{raw: raw, budget: budget, passed: banked.PassedTests}
								groupBudgetMu.Unlock()
								reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationOracleBudget, Symbol: tg.Symbol, Package: group.pkgs[0], OracleBudget: budget.String()})
							} else {
								groupBudgetMu.Lock()
								groupBaselines[key] = groupBaseline{raw: raw, passed: banked.PassedTests}
								groupBudgetMu.Unlock()
							}
							state, ok = adopted, true
							baselineCache[key] = state
						}
					}
				}
			}
			if !ok {
				reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0]})
				if err := ctx.Err(); err != nil {
					return "", err
				}
				// The measurement leash, lifted by the group's banked
				// duration when the bank holds one whose pins no longer
				// serve (the entry still says how long this oracle takes).
				baselineBound := opts.OracleTimeout
				if deriveOracleBudgets {
					baselineBound = leashFor(opts.OracleTimeout, time.Duration(banked.RawMillis)*time.Millisecond)
				}
				baselineStart := time.Now()
				probeGate.RLock()
				baseline, err := seams.baselineProbe(ctx, t.dir, group.pkgs[0], group.runRegex, baselineBound, group.flags, group.moduleDir, group.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
				probeGate.RUnlock()
				ran, passed, failedTests, diagnostic, observed := baseline.Ran, baseline.Passed, baseline.Failed, baseline.Diagnostic, baseline.Observation
				baselineElapsed := time.Since(baselineStart)
				var reason string
				switch {
				case err != nil && ctx.Err() != nil:
					return "", ctx.Err()
				case err != nil:
					reason = fmt.Sprintf("oracle baseline probe failed in %s: %v", group.pkgs[0], err)
				case ran == 0:
					reason = fmt.Sprintf("oracle baseline matched no tests in %s", group.pkgs[0])
				case !passed:
					reason = fmt.Sprintf("oracle baseline does not pass in %s (failed: %s)", group.pkgs[0], strings.Join(failedTests, ", "))
				}
				// What the oracle saw rides the analysis channel as a
				// payload-bearing event beside the skip decision — the
				// decision line names the tests, the event carries their
				// output — for a reported failure and for a result that
				// drifted between the discovery and measurement runs alike,
				// so a baseline failing under the campaign and passing for
				// the caller's plain go test is diagnosable from the run
				// (REQ-exec-run-status).
				if reason != "" && diagnostic != "" && opts.AnalysisEvent != nil {
					opts.AnalysisEvent(AnalysisEvent{Phase: "baseline-output", Package: group.pkgs[0], Detail: diagnostic})
				}
				// A refusing CONSUMER package of a derived oracle names
				// the escape: the oracle-of-record is a derivation-time
				// fact, so the group is never silently dropped (record
				// identity must not follow transient suite health) —
				// the caller fixes the suite or narrows explicitly
				// (REQ-target-default's cross-package baseline clause).
				if reason != "" && len(tg.Oracle) == 0 && !tg.OracleExplicit && w.targetView != nil && group.pkgs[0] != w.targetView.subject.Package {
					reason += fmt.Sprintf(" — a consumer package of the derived oracle; fix its suite, or pass an explicit oracle to measure %s without it", tg.Symbol)
				}
				if reason != "" {
					baselineFailures[key] = reason
					return reason, nil
				}
				// Only a PASSING baseline reaches here — a failing or
				// refused one returned above and records nothing (a
				// baseline dying at the leash must not register a
				// leash-multiple budget for its group's siblings, and
				// prices nothing). The raw wall-clock records in every
				// budget mode; the derived budget only in derive mode.
				entry := groupBaseline{raw: baselineElapsed, passed: baseline.Tests}
				if deriveOracleBudgets {
					entry.budget = derivedOracleBudget(baselineElapsed)
				}
				groupBudgetMu.Lock()
				groupBaselines[key] = entry
				groupBudgetMu.Unlock()
				// The deposit is PENDING (REQ-result-baseline-bank): a
				// clean, verifiable, passing probe records what it
				// measured; the pins — observation-bearing evidence
				// rows — are built once by the finding assembly, so the
				// deposit completes at commit time from the finding's
				// own rows. The unverifiable guard is belt-and-braces:
				// a banked unverifiable row could never SERVE anyway
				// (bankPinsHold runs the finding-serve pair discipline,
				// whose accept refuses unverifiable evidence) — the
				// guard only keeps dead weight out of the bank. The
				// manifest guard is load-bearing: adoption needs one.
				if !observed.Unverifiable && observed.Manifest != "" {
					deposited, derr := depositGroupBaseline(ctx, opts.baselineBank, key.persistedKey(bankScope), *w, group, observed, runEnv, baselineElapsed, baseline.Tests)
					if derr != nil {
						return "", derr
					}
					if !deposited {
						groupBudgetMu.Lock()
						pendingBankDeposits[key] = pendingBankDeposit{manifest: observed.Manifest, digest: observed.Digest, raw: baselineElapsed, passed: baseline.Tests}
						groupBudgetMu.Unlock()
					}
				}
				if deriveOracleBudgets {
					reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationOracleBudget, Symbol: tg.Symbol, Package: group.pkgs[0], OracleBudget: entry.budget.String()})
				}
				state = observed
				if err := ctx.Err(); err != nil {
					return "", err
				}
				baselineCache[key] = state
			}
			w.baselines = append(w.baselines, state)
			groupBudgetMu.Lock()
			w.groups[gi].passed = groupBaselines[key].passed
			groupBudgetMu.Unlock()
		}
		return "", nil
	}
	// prepareShaped is the shaped lane of target preparation: wholesale
	// serve-or-remeasure on the shape digest and oracle evidence, then
	// the same per-package oracle groups and baselines every measure
	// target earns (REQ-target-structural, REQ-target-manual-recipes).
	prepareShaped := func(ctx context.Context, resolved resolvedTarget) (*work, error) {
		i := resolved.index
		tg := targets[i]
		f := &findings[i]
		mv := modeFor(resolved.attested)
		oracle := resolved.oracle
		oracleViews := make([]*subjectView, 0, len(oracle))
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, mv.views.bySymbol[symbol])
		}
		rec, hasPrior := prior[tg.Symbol]
		// The property-runtime regime is a measurement pin for shaped
		// findings exactly as for symbol findings: a rapid oracle
		// package pins its draws (REQ-exec-property-oracles). A
		// detection failure (a go list spawn failing under load) is
		// this target's own condition, never a campaign abort — the
		// same locality every other preparation probe keeps
		// (REQ-exec-quiescence).
		targetRapid, err := preparation.rapidPackages(ctx, oraclePackages)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipTarget(i, "property-runtime detection unavailable: "+err.Error(), true)
			return nil, nil
		}
		regime := ""
		for _, run := range pkgRuns(oracle) {
			if targetRapid[run.pkg] {
				regime = engine.PropertyRegimeRapid
				break
			}
		}
		f.PropertyRegime = regime
		f.OracleExecutionPolicy = FullOracleExecutionPolicy
		reason := "no-prior"
		var decision RunDecision
		if hasPrior && opts.Force {
			reason = "forced"
		}
		if hasPrior && !opts.Force {
			matches, err := shapedEvidenceMatchesContext(ctx, *rec, oracleViews, shapedOperatorSet, opts.OracleTimeout.String(), deriveOracleBudgets, oracleMemoryPin, regime)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				refuseSkipped(i, serveCheckRefusal(err), true)
				return nil, nil
			}
			if matches && rec.BodyHash == resolved.shapedDigest && shapeEqual(rec.Shape, &TargetShape{Structural: tg.Structural, Manual: tg.Manual}) {
				// Wholesale serve: shaped findings take no splice
				// carve-outs — any moved pin re-measures every
				// candidate, and the candidate sets are small by
				// construction (REQ-result-stale). Provenance
				// re-stamps and staged drift refuses exactly as a
				// symbol serve (REQ-result-layers).
				served := snapshotFindings([]Finding{*rec})[0]
				served.Selection = selKey
				served.Labels = append([]string(nil), tg.Labels...)
				served.Cached = true
				if stagedDrift, err := t.stampProvenance(ctx, repository, nil, oracleViews, resolved.shapedFiles, &served); err != nil {
					return nil, err
				} else if stagedDrift != "" {
					refuseSkipped(i, "staged drift: "+stagedDrift, true)
					return nil, nil
				}
				served = annotateCrossSelection(served)
				*f = served
				cacheTarget(i, "served: shape and oracle pins unchanged"+resolved.oracleNote)
				return nil, nil
			}
			reason = "stale"
			if issue := OracleExecutionPolicyIssue(*rec); issue != "" {
				reason = "stale: " + issue
			} else if !matches {
				reason = "stale: an oracle or measurement pin moved"
			} else if rec.BodyHash != resolved.shapedDigest {
				reason = "stale: the declared shape or a probed file moved"
			}
		}
		// The measure path attaches evidence, so it needs the OBSERVED
		// producer views — the decision views above serve only the pin
		// checks; a shaped identity is not in the union, so the subset
		// derives from the oracle symbols alone.
		if err := preflightOracleModules(oracle); err != nil {
			return nil, err
		}
		// A plan pays no observed union: its decision derives from the
		// decision views' pin checks and the shape's own candidates, and
		// the union only attaches execution evidence
		// (REQ-exec-plan-only).
		var producerViews *subjectViewSet
		if !opts.PlanOnly {
			views, fault, err := producerViewsFor(unitOf[i], oracle[0], oracle[1:], oracle)
			if err != nil {
				return nil, err
			}
			if fault != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				skipTarget(i, "producer evidence unavailable: "+fault.Error(), true)
				return nil, nil
			}
			producerViews = views
		}
		// Producer enrollment mirrors the symbol path's: the global
		// modules enter end-of-run producer validation so the epilogue's
		// transient global-drift report covers shaped-only campaigns too;
		// the narrowed sibling modules below get their own per-window
		// validation. Enrollment happens only past the serve return, so a
		// served or skipped shaped target never turns a target-local
		// condition into a campaign-level drift report.
		for _, oracleView := range oracleViews {
			oracleView.module.producer = true
		}
		if producerViews != nil {
			oracleViews = oracleViews[:0]
			for _, symbol := range oracle {
				oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
			}
			for _, module := range producerViews.modules {
				module.producer = true
			}
		}
		item := work{target: i, oracle: oracle, reason: reason, shaped: true, candidates: resolved.shaped, shapedFiles: resolved.shapedFiles, oracleViews: oracleViews, producer: producerViews}
		w := &item
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		w.oracleSet = oracleSet
		decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason + resolved.oracleNote, Candidates: len(w.candidates)}
		f.Budget = 0
		f.CandidateCount = len(w.candidates)
		f.Generated = len(w.candidates)
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
		if opts.PlanOnly {
			if opts.Decision != nil {
				opts.Decision(decision)
			}
			return nil, nil
		}
		runs := pkgRuns(w.oracle)
		runtimes, err := preparation.propertyRuntimes(ctx, oraclePackages)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipTarget(i, "property-runtime detection unavailable: "+err.Error(), true)
			return nil, nil
		}
		for _, pr := range runs {
			flags := propertyOracleFlags(slices.Contains(runtimes[pr.pkg], "rapid"))
			// The shaped lane states what it pins exactly as the
			// symbol lane's group construction does: a shaped-only
			// campaign over a property package must not pin silently
			// (REQ-exec-property-oracles' once-per-package statement).
			for _, runtime := range runtimes[pr.pkg] {
				note, ok := propertyOracleNote(pr.pkg, runtime)
				if ok && preparation.noteProperty(pr.pkg+"/"+runtime) && opts.PropertyOracle != nil {
					opts.PropertyOracle(note)
				}
			}
			moduleDir, packageDir, err := preparation.packageContext(ctx, pr.pkg)
			if err != nil {
				return nil, err
			}
			w.groups = append(w.groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		if reason, err := probeGroupBaselines(ctx, tg, w); err != nil {
			return nil, err
		} else if reason != "" {
			skipTarget(i, reason, true)
			return nil, nil
		}
		if opts.Decision != nil {
			opts.Decision(decision)
		}
		preparedTargets.Add(1)
		preparedCandidates.Add(int64(len(w.candidates)))
		return w, nil
	}
	prepareTarget := func(ctx context.Context, resolved resolvedTarget) (*work, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i := resolved.index
		tg := targets[i]
		f := &findings[i]
		mv := modeFor(resolved.attested)
		oracle := resolved.oracle
		targetView := mv.views.bySymbol[tg.Symbol]
		oracleViews := make([]*subjectView, 0, len(oracle))
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, mv.views.bySymbol[symbol])
		}
		// A staged run's one external-input judgment, before the shaped
		// dispatch: both preparation paths derive their provenance inputs
		// from these views (a shaped target has no target view of its
		// own, and its probed files ride resolved.shapedFiles).
		if repository.staged {
			external, unlisted, err := t.externalInputs(ctx, repository, targetView, oracleViews, resolved.shapedFiles)
			if err != nil {
				return nil, err
			}
			if len(unlisted) != 0 {
				// The historical listing is a provenance input the
				// snapshot cannot vouch for when it fails: the target
				// refuses with the failure named (REQ-exec-quiescence).
				refuseSkipped(i, unlistedRefusal(unlisted, "; measure unstaged"), true)
				return nil, nil
			}
			if len(external) != 0 {
				refuseSkipped(i, "measured input outside the repository: "+cappedJoin(external, 8)+" - the index snapshot cannot vouch for it; measure unstaged", true)
				return nil, nil
			}
		}
		if resolved.shaped != nil {
			return prepareShaped(ctx, resolved)
		}
		// driftSkip converts a source-drift refusal from candidate
		// generation into the contracted target-local skip: the tree
		// moved under the run, so this target refuses with the drift
		// named while completed siblings keep their findings
		// (REQ-exec-quiescence); a true generator fault still aborts.
		driftSkip := func(err error) bool {
			var sourceDrift *engine.SourceDriftError
			if !errors.As(err, &sourceDrift) {
				return false
			}
			refuseSkipped(i, "source changed since load: "+sourceDrift.Path+" - re-run when the tree settles", true)
			return true
		}
		rec, hasPrior := prior[tg.Symbol]
		// The target's property-runtime measurement regime: a rapid
		// oracle package pins its draws, and the regime is a measurement
		// pin - a record measured under other draws re-measures
		// (REQ-exec-property-oracles). A detection failure is this
		// target's own condition, never a campaign abort
		// (REQ-exec-quiescence's locality).
		targetRapid, err := preparation.rapidPackages(ctx, oraclePackages)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipTarget(i, "property-runtime detection unavailable: "+err.Error(), true)
			return nil, nil
		}
		regime := ""
		for _, run := range pkgRuns(oracle) {
			if targetRapid[run.pkg] {
				regime = engine.PropertyRegimeRapid
				break
			}
		}
		f.PropertyRegime = regime
		f.OracleExecutionPolicy = FullOracleExecutionPolicy
		reason := "no-prior"
		if hasPrior {
			switch {
			case opts.Force:
				reason = "forced"
			case !budgetCovers(*rec, opts.Budget):
				reason = fmt.Sprintf("budget: prior generated %d of %d candidates, request wants budget %d", rec.Generated, rec.CandidateCount, opts.Budget)
			default:
				reason = "stale"
			}
		}
		var serve *Finding
		// refuseServeCheck is the serve block's target-local exit for a
		// failed evidence check (serveCheckRefusal, any non-ctx error):
		// the condition is this target's own, the campaign proceeds.
		refuseServeCheck := func(reason string) {
			refuseSkipped(i, reason, true)
		}
		if hasPrior && !opts.Force && budgetCovers(*rec, opts.Budget) {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), deriveOracleBudgets, oracleMemoryPin, regime)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				refuseServeCheck(serveCheckRefusal(err))
				return nil, nil
			}
			if !matches {
				// A named killer is not an independence proof: another test
				// can alter its shared state, and an added test can un-kill it.
				// Keep the actual refusal attribution and remeasure the target
				// against its complete current oracle (REQ-result-stale).
				probeGate.RLock()
				reason = t.movedPinAttribution(ctx, *rec, mv.views, "stale: a measurement pin moved (oracle timeout, oracle selection, operator set, or runtime inputs moved during evaluation)")
				probeGate.RUnlock()
			}
			if matches && len(rec.CandidateEvidence) == 0 {
				cached := *rec
				cached.Selection = selKey
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
				// Serving retains the historical ledger, including absent
				// binding evidence. Current analysis cannot backfill it.
				// The serve's freshness proof validated every subject's
				// evidence against the run-start view capture, so provenance
				// recomputes like a fresh measure's - a dirty-born record
				// promotes to the portable layer once its paths are clean
				// (REQ-result-stale, REQ-result-layers). A serve's evidence checks
				// re-observe the view against disk when they close over the
				// record's required runtime-input evidence, so a content
				// move past the capture surfaces at the checks above and
				// classifies target-locally (serveCheckRefusal) — no
				// separate ref-motion gate is needed
				// (REQ-exec-quiescence).
				if stagedDrift, err := t.stampProvenance(ctx, repository, targetView, oracleViews, nil, &cached); err != nil {
					return nil, err
				} else if stagedDrift != "" {
					refuseSkipped(i, "staged drift: "+stagedDrift, true)
					return nil, nil
				}
				cached = annotateCrossSelection(cached)
				findings[i] = cached
				// Commit precedes the decision: a caller canceling at the
				// decision callback still holds the persisted finding
				// (REQ-exec-cancellation's incremental-commit clause).
				if err := commitFinding(ctx, opts.Commit, cached); err != nil {
					return nil, err
				}
				cacheTarget(i, "served: body, oracle closure, and runtime inputs unchanged"+resolved.oracleNote)
				return nil, nil
			}
			if matches {
				// The record's only unverifiable runtime evidence is
				// candidate-local: serve its covered candidates and
				// re-execute exactly the flagged ones under a passing
				// current baseline probe (REQ-result-stale). Candidate
				// regeneration below decides whether the splice can proceed.
				snapshot := snapshotFindings([]Finding{*rec})[0]
				serve = &snapshot
			}
		}
		var extend *Finding
		if hasPrior && !opts.Force && !budgetCovers(*rec, opts.Budget) {
			if len(rec.CandidateEvidence) != 0 {
				// The budget extension never composes with the
				// candidate-evidence serve: the flagged splice re-identifies
				// candidates under the record's own selection, so a wider
				// request re-measures whole rather than mixing the two
				// disciplines (REQ-result-stale).
				reason += "; prior candidate evidence re-executes only under its recorded budget, so the whole target re-measures"
				if issue := OracleExecutionPolicyIssue(*rec); issue != "" {
					reason += "; " + issue
				}
			} else {
				matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), deriveOracleBudgets, oracleMemoryPin, regime)
				if err != nil {
					return nil, err
				}
				if matches {
					// Every pin except the budget holds, so the record's
					// measured prefix stands as exact evidence for candidates
					// [0, generated) and only the unmeasured suffix executes
					// (REQ-mut-budget, REQ-result-stale's budget-extension
					// carve-out). Candidate regeneration below decides whether
					// the prefix re-identifies.
					snapshot := snapshotFindings([]Finding{*rec})[0]
					extend = &snapshot
				} else {
					// The budget was not the only moved pin, so the prefix
					// cannot stand; the appended attribution keeps the
					// re-measure reason honest (REQ-result-stale). Gated
					// shared like the other attribution site: the call may
					// build a supplementary view.
					probeGate.RLock()
					attribution := t.movedPinAttribution(ctx, *rec, mv.views, "a measurement pin also moved, so the measured prefix cannot stand")
					probeGate.RUnlock()
					reason += "; " + attribution
				}
			}
		}

		if err := preflightOracleModules(oracle); err != nil {
			return nil, err
		}
		// A plan pays no observed union: the decision below derives from
		// the decision views and the candidate generation alone, and the
		// union only attaches execution evidence (REQ-exec-plan-only).
		var producerViews *subjectViewSet
		if !opts.PlanOnly {
			views, fault, err := producerViewsFor(unitOf[i], tg.Symbol, oracle, append([]string{tg.Symbol}, oracle...))
			if err != nil {
				return nil, err
			}
			if fault != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					// The campaign itself is canceled: abort is the answer.
					return nil, proofAbortError(tg.Symbol, oracle, fault, ctxErr)
				}
				// A per-target evidence condition, target-local by the same
				// rule as drift refusal (REQ-exec-quiescence): this target
				// skips with the cause on its decision line — a skip never
				// overwrites a prior record — and the campaign proceeds.
				skipTarget(i, fmt.Sprintf("freshness proof unavailable (oracle %s): %v", strings.Join(oracle, ", "), fault), true)
				return nil, nil
			}
			producerViews = views
		}
		// Producer enrollment happens only for targets whose proof
		// exists: enrolling before the build would put a skipped
		// target's module into end-of-run producer validation, turning a
		// target-local condition into a campaign-level drift report; a
		// shared oracle module is enrolled by the measured sibling
		// itself.
		targetView.module.producer = true
		for _, oracleView := range oracleViews {
			oracleView.module.producer = true
		}
		if opts.producer != nil {
			opts.producer(tg.Symbol)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if producerViews != nil {
			targetView = producerViews.bySymbol[tg.Symbol]
			oracleViews = oracleViews[:0]
			for _, symbol := range oracle {
				oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
			}
			for _, module := range producerViews.modules {
				module.producer = true
			}
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		currentLedger, err := targetView.view.TestVariantLedger(targetView.subject)
		if err != nil {
			return nil, err
		}
		item := work{target: i, oracle: oracle, reason: reason, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, producer: producerViews, currentLedger: currentLedger, serve: serve, extend: extend}
		w := &item
		var decision RunDecision
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		budget := opts.Budget
		if w.serve != nil {
			// Regenerate the served record's exact selected prefix so its
			// flagged candidates re-identify deterministically.
			budget = w.serve.Budget
		}
		// regenerateAtBudget re-enumerates at the request budget when a
		// carve-out's record-shaped regeneration cannot be spliced —
		// one exit for the retained reuse fallbacks (source drift skips
		// target-locally, any other failure aborts as ever).
		regenerateAtBudget := func() (engine.Generation, bool, error) {
			g, gerr := t.eng.CandidatesContext(ctx, tg.Symbol, opts.Budget)
			if gerr != nil {
				if driftSkip(gerr) {
					return engine.Generation{}, true, nil
				}
				return engine.Generation{}, false, fmt.Errorf("target %s: %w", tg.Symbol, gerr)
			}
			return g, false, nil
		}
		generation, err := t.eng.CandidatesContext(ctx, tg.Symbol, budget)
		if err != nil {
			if driftSkip(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
		}
		if w.serve != nil {
			if flagged, ok := flaggedCandidateIndexes(generation, *w.serve); ok {
				w.candidates = generation.Candidates
				w.flagged = flagged
				decision = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: fmt.Sprintf("served: pins unchanged; re-executing %s", candidateNoun(len(flagged))) + resolved.oracleNote, Candidates: len(flagged)}
			} else {
				// Deterministic regeneration cannot re-identify every flagged
				// candidate and recorded survivor, so the record cannot be
				// spliced: the whole target re-measures (REQ-result-stale).
				w.serve, w.flagged = nil, nil
				if budget != opts.Budget {
					var regenerated bool
					if generation, regenerated, err = regenerateAtBudget(); regenerated {
						return nil, nil
					} else if err != nil {
						return nil, err
					}
				}
			}
		}
		if w.extend != nil {
			if extendedPrefixStands(generation, *w.extend) {
				w.candidates = generation.Candidates
				w.extendFrom = w.extend.Generated
				suffix := len(generation.Candidates) - w.extendFrom
				decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: fmt.Sprintf("served: prefix of %s stands; measuring %d more", candidateNoun(w.extendFrom), suffix) + resolved.oracleNote, Candidates: suffix}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// prefix, so the record cannot be extended: the whole target
				// re-measures (REQ-result-stale).
				w.extend = nil
				w.reason += "; deterministic regeneration cannot re-identify the measured prefix"
			}
		}
		if w.serve == nil && w.extend == nil {
			w.candidates = generation.Candidates
			decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: w.reason + resolved.oracleNote, Candidates: len(generation.Candidates)}
			f.Budget = opts.Budget
			f.CandidateCount = generation.CandidateCount
			f.Generated = len(generation.Candidates)
		}
		// Per-package oracle scoping (REQ-exec-oracle-run), with the rapid
		// failfile flag only in front of binaries that register it
		// (REQ-mut-overlay). Every complete current group earns its own
		// baseline before any mutant runs.
		if opts.PlanOnly {
			// The plan needs candidate counts and decisions, never
			// baseline probes: group construction and probing are
			// execution cost the plan exists to preview.
			if opts.Decision != nil {
				opts.Decision(decision)
			}
			return nil, nil
		}
		runtimes, err := preparation.propertyRuntimes(ctx, oraclePackages)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipTarget(i, "property-runtime detection unavailable: "+err.Error(), true)
			return nil, nil
		}
		buildGroups := func(oracle []string) ([]group, error) {
			var groups []group
			for _, pr := range pkgRuns(oracle) {
				// Flags and statements derive from the one detection scan:
				// a package whose runtimes include rapid pins, and each
				// detected runtime earns its own statement
				// (REQ-exec-property-oracles).
				flags := propertyOracleFlags(slices.Contains(runtimes[pr.pkg], "rapid"))
				for _, runtime := range runtimes[pr.pkg] {
					note, ok := propertyOracleNote(pr.pkg, runtime)
					if ok && preparation.noteProperty(pr.pkg+"/"+runtime) && opts.PropertyOracle != nil {
						opts.PropertyOracle(note)
					}
				}
				moduleDir, packageDir, err := preparation.packageContext(ctx, pr.pkg)
				if err != nil {
					return nil, err
				}
				groups = append(groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
			}
			return groups, nil
		}
		if w.groups, err = buildGroups(w.oracle); err != nil {
			return nil, err
		}
		if reason, err := probeGroupBaselines(ctx, tg, w); err != nil {
			return nil, err
		} else if reason != "" {
			skipTarget(i, reason, true)
			return nil, nil
		}
		// The target's own preparation events — mutants and baselines —
		// all precede its decision (REQ-exec-run-status).
		if opts.Decision != nil {
			opts.Decision(decision)
		}
		preparedTargets.Add(1)
		preparedCandidates.Add(int64(len(w.candidates)))
		return w, nil
	}

	// deliverPrepared walks every target in order — streaming resolve-phase
	// skip decisions and preparing resolved targets — so the decision
	// stream's order is exactly the preparation sequence's target order
	// (REQ-exec-run-status). emit receives each measure target's work item.
	deliverPrepared := func(ctx context.Context, emit func(work) error) error {
		next := 0
		for i := range targets {
			if next < len(resolvedTargets) && resolvedTargets[next].index == i {
				w, err := prepareTarget(ctx, resolvedTargets[next])
				next++
				if err != nil {
					return err
				}
				if w != nil && emit != nil {
					if err := emit(*w); err != nil {
						return err
					}
				}
				continue
			}
			if decisions[i].Action != "" && opts.Decision != nil {
				opts.Decision(decisions[i])
			}
		}
		return nil
	}

	if opts.PlanOnly {
		if err := deliverPrepared(ctx, nil); err != nil {
			return nil, err
		}
		// A plan is a decision about committing budget, so it refuses
		// on the same tree-motion evidence an executing run's epilogue
		// checks — neither is a baseline probe or a mutant execution.
		// Ref motion alone is not tree motion: capture commits are read
		// at stamp time, so only content drift refuses
		// (REQ-exec-quiescence).
		if err := eachMode(func(mv *modeViews) error {
			// The plan re-establishes no proof — it pays no observed
			// union, so its decision views validate their base facts
			// alone — and the analysis budget cannot cut it: the
			// campaign's passes are the budget's whole reach
			// (REQ-exec-analysis-budget).
			return mv.views.validateProducers(ctx)
		}); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &TreeDriftError{Transient: err.Error()}
		}
		// A staged drift refused during preparation surfaces exactly as
		// an executing run's epilogue would surface it - a plan
		// delivering success while silently dropping a refused target
		// would misreport the budget decision (REQ-exec-plan-only,
		// REQ-result-staged).
		driftedMu.Lock()
		planDrifted := append([]TargetDrift(nil), drifted...)
		driftedMu.Unlock()
		if len(planDrifted) > 0 {
			return nil, &TreeDriftError{Drifted: planDrifted}
		}
		// Only findings complete without execution return (cached
		// serves and skips), so a partially-enumerated measure target
		// never escapes as evidence.
		var planned []Finding
		for i := range findings {
			if findings[i].Cached || findings[i].Skipped != "" {
				planned = append(planned, findings[i])
			}
		}
		return planned, nil
	}

	// Phase two: preparation pipelined with the pool. A single preparation
	// goroutine drives prepareTarget serially in target order — the
	// deterministic preparation-and-decision sequence is its alone — and
	// feeds ready work items to the window loop below, so execution
	// overlaps only later targets' preparation (REQ-exec-run-status). The
	// buffer holds every possible item, so preparation never blocks behind
	// execution; clean-tree probes beside executing mutants are sound
	// because a mutant runs through its own overlay and never touches the
	// tree (REQ-mut-overlay).
	items := make(chan work, len(targets))
	var prepErr error
	prepDone := make(chan struct{})
	runCtx, cancelRun := context.WithCancel(ctx)
	// The join runs on every return path: no caller callback may fire
	// after Run returns (the synchronous-caller-code contract), and a
	// canceled run still waits for producer-side probe cleanup
	// (REQ-exec-cancellation). Receiving from the closed prepDone is
	// idempotent, so the deferred join composes with the loop's own read.
	defer func() {
		cancelRun()
		<-prepDone
	}()
	go func() {
		defer close(prepDone)
		defer close(items)
		delivered := 0
		err := deliverPrepared(runCtx, func(w work) error {
			if seams.truncateAfterItems > 0 && delivered >= seams.truncateAfterItems {
				return errTruncateSeam
			}
			select {
			case items <- w:
				delivered++
				return nil
			case <-runCtx.Done():
				return runCtx.Err()
			}
		})
		if errors.Is(err, errTruncateSeam) {
			// The seam models the failure REQ-exec-completion exists
			// for: a pipeline that ends early with its error lost.
			// With seams.truncateErr set it models the OTHER failure - a
			// preparation error that surfaces - so the error path's
			// held-window aggregation is testable.
			err = seams.truncateErr
		}
		prepErr = err
	}()
	interner := &manifestInterner{byDigest: map[string]string{}}
	// A job carries the already-scoped work: the dispatch loop derives it
	// once and the worker executes that value; the executor emits the
	// scope it runs through the executedScope seam off the same variable.
	type job struct {
		wi, mi int
		w      work
	}
	// Execution proceeds in bounded target windows: each window's pool
	// drains, its kills confirm serially against an idle pool, and its
	// findings aggregate and COMMIT before the next window dispatches
	// (REQ-exec-cancellation's incremental-commit clause). One
	// campaign-global pool would hold every target's observations
	// resident until the end and commit nothing for hours, so a single
	// late abort cost the whole campaign's verdicts. Serial
	// confirmation's isolation contract is "alone after the pool
	// drains", which the per-window drain preserves.
	// The window's bounds are content-stable — a function of the tree's
	// candidate counts, the target order, the worker count, and the
	// run's derived oracle lists (a stood-down derivation shortens them
	// for that run alone), never of a measured duration — so the
	// partition never moves between runs of an unchanged tree under the
	// same workers and the same derivations
	// (REQ-exec-oracle-run's window rule).
	windowBudget, windowMinimum := windowcost.Bounds(jobs, seams.windowCandidates)
	var treeDrift error
	// commitAndAttribute is the one epilogue every measured or spliced
	// finding leaves aggregation through: install, persist, and — when
	// the evidence landed unverifiable under a package-derived oracle —
	// emit the oracle-instability attribution
	// (REQ-exec-oracle-guidance). One exit keeps the attribution
	// structurally coupled to the commit: no serve arm can persist an
	// unverifiable record silently.
	// committed is the completion ledger for measured targets: a clean
	// return vouches for the whole announced roster, and this ledger is
	// how the vouch is checked - phase-one serves and skips carry their
	// own marks (Cached, Skipped), drifted targets are named in the
	// drift error, and everything else must pass through here.
	committed := make([]bool, len(targets))
	commitAndAttribute := func(ctx context.Context, f Finding, w work) error {
		f.Selection = selKey
		f = annotateCrossSelection(f)
		findings[w.target] = f
		if err := commitFinding(ctx, opts.Commit, f); err != nil {
			return err
		}
		committed[w.target] = true
		targetDone(w.target)
		// Complete pending bank deposits (REQ-result-baseline-bank):
		// the committed finding's OracleEvidence rows are the pins —
		// the exact observation-bearing rows finding freshness
		// compares, so a banked serve inherits the finding-serve
		// semantics verbatim.
		for _, g := range w.groups {
			key := keyFor(g)
			groupBudgetMu.Lock()
			pd, pending := pendingBankDeposits[key]
			groupBudgetMu.Unlock()
			if !pending {
				continue
			}
			rows := bankOracleRowsFor(f, g.pkgs[0])
			if len(rows) == 0 {
				continue
			}
			opts.baselineBank.putBaseline(key.persistedKey(bankScope), bankedBaseline{
				Evidence: rows, Manifest: pd.manifest, Digest: pd.digest,
				PassedTests: pd.passed,
				RawMillis:   pd.raw.Milliseconds(), MeasuredAtUnix: time.Now().Unix(),
			})
			groupBudgetMu.Lock()
			delete(pendingBankDeposits, key)
			groupBudgetMu.Unlock()
		}
		return t.emitOracleGuidance(ctx, f, w, targets[w.target].Symbol, opts, runEnv, guidanceCache)
	}
	// Advisory execution progress rides window boundaries and
	// per-candidate ticks: totals are exact, per-window timing is not
	// part of the deterministic sequence (REQ-exec-run-status's
	// advisory classes). mutantsDone is atomic because workers tick it
	// as candidates finish; windows execute serially, so the boundary
	// true-up (Store) never races a tick.
	dispatchedTargets := 0
	var mutantsDone atomic.Int64
	// tickMu serializes the counter advance WITH its event delivery:
	// without it two workers can Add in one order and deliver in the
	// other, and the tick stream — pinned monotone — regresses.
	// Ordering is tickMu → the callback lock, never inverted.
	var tickMu sync.Mutex
	type executedWindow struct {
		window       []work
		observations [][]runtimeinput.Observation
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
	executeWindow := func(window []work) (*executedWindow, error) {
		outcomes := make([][]engine.MutantOutcome, len(window))
		flips := make([]map[int]string, len(window))
		observations := make([][]runtimeinput.Observation, len(window))
		incompletes := make([][]string, len(window))
		killers := make([][]string, len(window))
		memoryDecided := make([][]bool, len(window))
		for wi := range window {
			outcomes[wi] = make([]engine.MutantOutcome, len(window[wi].candidates))
			observations[wi] = make([]runtimeinput.Observation, len(window[wi].candidates))
			incompletes[wi] = make([]string, len(window[wi].candidates))
			killers[wi] = make([]string, len(window[wi].candidates))
			memoryDecided[wi] = make([]bool, len(window[wi].candidates))
		}
		// score is the window's ONE score writer: every measurement of a
		// candidate — the parallel worker's or serial confirmation's —
		// records its channels through it, so a
		// measurement cannot replace the verdict and keep another
		// channel's stale value.
		// The outcome is written last: an unwritten outcome reads as
		// the discard zero value, never as a verdict, so it is the
		// row's sentinel and the other channels are in place before it.
		score := func(wi, mi int, outcome engine.MutantOutcome, killer string, decided bool, state runtimeinput.Observation, incomplete string) {
			killers[wi][mi] = killer
			memoryDecided[wi][mi] = decided
			observations[wi][mi] = interner.intern(state)
			incompletes[wi][mi] = incomplete
			outcomes[wi][mi] = outcome
		}
		windowBase := mutantsDone.Load()
		reportExecuting(opts.Executing, ExecutionEvent{
			Phase:       "executing",
			TargetIndex: dispatchedTargets + 1, TargetCount: int(preparedTargets.Load()),
			Symbol:         targets[window[0].target].Symbol,
			CandidatesDone: int(windowBase), CandidatesTotal: int(preparedCandidates.Load()),
		})
		// Price complete groups; coverage carries no omission authority.
		est := estimateWindow(window, opts.baselineDur)
		reportExecuting(opts.Executing, ExecutionEvent{
			Phase:       "estimate",
			TargetIndex: dispatchedTargets + 1, TargetCount: int(preparedTargets.Load()),
			Symbol:         targets[window[0].target].Symbol,
			CandidatesDone: int(windowBase), CandidatesTotal: int(preparedCandidates.Load()),
			EstimateFull: est.full, EstimateUnknown: est.unknown,
			EstimateProjected: est.projectedString(),
		})
		jobCh := make(chan job)
		poolCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var wg sync.WaitGroup
		var errOnce sync.Once
		var poolErr error
		for range jobs {
			if err := poolCtx.Err(); err != nil {
				break
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobCh {
					if poolCtx.Err() != nil {
						return
					}
					w := j.w
					m, runnable := w.candidates[j.mi].Mutant()
					if !runnable {
						continue
					}
					outcome, killer, candidateMemoryDecided, state, incompleteReason, err := t.executeWorkMutant(poolCtx, w, m, opts, runEnv)
					if err != nil {
						if poolCtx.Err() != nil {
							return
						}
						errOnce.Do(func() {
							poolErr = err
							cancel()
						})
						return
					}
					score(j.wi, j.mi, outcome, killer, candidateMemoryDecided, state, incompleteReason)
					// The completion tick: done advances candidate by
					// candidate instead of window by window, the pace
					// ground a multi-hour window would otherwise hide
					// (REQ-exec-run-status's estimate class). tickMu
					// binds the Add to its delivery — without it a
					// preempted worker delivers a smaller done AFTER a
					// sibling's larger one and the pinned-monotone
					// stream regresses.
					tickMu.Lock()
					reportExecuting(opts.Executing, ExecutionEvent{
						Phase:       "tick",
						TargetIndex: dispatchedTargets + j.wi + 1, TargetCount: int(preparedTargets.Load()),
						Symbol:         w.candidates[j.mi].Symbol,
						CandidatesDone: int(mutantsDone.Add(1)), CandidatesTotal: int(preparedCandidates.Load()),
					})
					tickMu.Unlock()
				}
			}()
		}
		// The graceful drain (REQ-exec-cancellation's graceful-interrupt
		// clause): a fired SoftStop stops admitting candidates; every
		// already-dispatched mutant finishes under the still-live pool
		// context, and prefixes records how far each work's candidate
		// walk got — the contiguous handled prefix, the capped-record
		// boundary the truncation below commits (-1 = fully handled).
		softStopped := false
		breakWi := len(window)
		prefixes := make([]int, len(window))
		for wi := range prefixes {
			prefixes[wi] = -1
		}
	dispatching:
		for wi := range window {
			for mi := range window[wi].candidates {
				select {
				case <-opts.SoftStop:
					softStopped = true
					prefixes[wi], breakWi = mi, wi
					break dispatching
				default:
				}
				// executesCandidate is the ONE candidate selection —
				// serve/extension/drift carve-outs (REQ-result-stale)
				// and pre-execution discards — shared with the window
				// cost model; the walk still visits every index so the
				// drain prefix can land on a skipped candidate exactly
				// as it always has.
				if !executesCandidate(window[wi], mi) {
					continue
				}
				if opts.dispatched != nil {
					opts.dispatched(window[wi].candidates[mi].Symbol, mi)
				}
				select {
				case jobCh <- job{wi, mi, window[wi]}:
				case <-opts.SoftStop:
					softStopped = true
					prefixes[wi], breakWi = mi, wi
					break dispatching
				case <-poolCtx.Done():
					break dispatching
				}
			}
		}
		close(jobCh)
		wg.Wait()
		if poolErr != nil {
			return nil, poolErr
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var discard []bool
		if softStopped {
			// The drained window truncates before confirmation and
			// assembly: each fresh work keeps its handled candidate
			// prefix and restamps as candidate-capped — the committed
			// record is indistinguishable from a budget-K run's, so its
			// reuse follows the budget-extension carve-out exactly and a
			// re-run measures only the remainder. Serve, splice, and
			// drift work, and a fresh work with an empty prefix, discard
			// whole — their prior records stand
			// (REQ-exec-cancellation's graceful-interrupt clause).
			discard = make([]bool, len(window))
			for wi := range window {
				k := prefixes[wi]
				if wi > breakWi {
					k = 0
				}
				if k < 0 {
					continue
				}
				if window[wi].serve != nil || window[wi].extend != nil || k == 0 {
					discard[wi] = true
					window[wi].candidates = nil
					outcomes[wi], observations[wi], incompletes[wi], killers[wi], memoryDecided[wi] = nil, nil, nil, nil, nil
					continue
				}
				window[wi].candidates = window[wi].candidates[:k]
				outcomes[wi] = outcomes[wi][:k]
				observations[wi] = observations[wi][:k]
				incompletes[wi] = incompletes[wi][:k]
				killers[wi] = killers[wi][:k]
				memoryDecided[wi] = memoryDecided[wi][:k]
				f := &findings[window[wi].target]
				f.Budget = k
				f.Generated = k
			}
		}
		// Serial kill confirmation (REQ-exec-attribution): a test-attributed
		// or package-scope kill measured beside sibling mutants re-executes
		// alone, and the serial execution is the scored one - outcome,
		// observation, and candidate evidence replaced wholesale - so
		// interference from a sibling never reads as a kill. Timeout kills
		// are excluded: confirming one costs the full timeout again, and the
		// hang bound is the caller's own budget - the named residual.
		// Boundary true-up: worker ticks counted executed candidates;
		// skipped, non-runnable, and served ones join here so done
		// converges on the prepared totals at every window boundary
		// (windows execute serially — the Store never races a tick).
		windowTotal := int64(0)
		for wi := range window {
			windowTotal += int64(len(window[wi].candidates))
		}
		mutantsDone.Store(advanceDone(windowBase, windowTotal, mutantsDone.Load()))
		// rescore is the window's serial confirmation discipline: the
		// candidate re-executes alone under the exclusively held probe
		// gate — so no pipelined sibling's preparation probe shares its
		// isolation window — and the scored serial run replaces the
		// window's verdict for it wholesale through the same score
		// writer the parallel workers use: outcome, killer, memory
		// disposition, observation, and incompleteness, as one value.
		// The serial execution replaces every channel of the parallel
		// measurement (REQ-exec-attribution).
		rescore := func(wi, mi int, run func(w work, m engine.Mutant) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, error)) (engine.MutantOutcome, string, error) {
			w := window[wi]
			m, _ := window[wi].candidates[mi].Mutant()
			opts.probeGate.Lock()
			outcome, killer, decided, state, incomplete, err := run(w, m)
			opts.probeGate.Unlock()
			if err != nil {
				return outcome, killer, err
			}
			score(wi, mi, outcome, killer, decided, state, incomplete)
			return outcome, killer, nil
		}
		if jobs > 1 {
			// Each confirmation re-runs a full oracle, so per-confirmation
			// events are naturally sparse; a window with nothing to
			// confirm reports nothing.
			confirmTotal := 0
			for wi := range window {
				for mi := range window[wi].candidates {
					if outcomes[wi][mi] == engine.MutantKilled && killers[wi][mi] != engine.TimeoutKiller {
						if _, runnable := window[wi].candidates[mi].Mutant(); runnable {
							confirmTotal++
						}
					}
				}
			}
			confirmDone := 0
			var kills []windowKill
			for wi := range window {
				for mi := range window[wi].candidates {
					if outcomes[wi][mi] != engine.MutantKilled || killers[wi][mi] == engine.TimeoutKiller {
						continue
					}
					if _, runnable := window[wi].candidates[mi].Mutant(); !runnable {
						continue
					}
					kills = append(kills, windowKill{target: wi, mi: mi})
				}
			}
			// Volatile evidence anywhere in a target's window — the
			// baseline or any mutant observation — is load sensitivity's
			// signature: that target's gate never samples. Snapshots are
			// per target at walk start; serial evidence arriving
			// unverifiable re-arms mid-walk through the walk's own
			// callback.
			volatileMemo := map[int]bool{}
			err := confirmWindowKills(
				func(wi int) bool {
					v, ok := volatileMemo[wi]
					if !ok {
						v = windowEvidenceVolatile(&window[wi], observations[wi])
						volatileMemo[wi] = v
					}
					return v
				},
				kills,
				func(k windowKill, confirmMode string) (confirmOutcome, error) {
					if err := ctx.Err(); err != nil {
						return confirmInconclusive, err
					}
					wi := k.target
					reportExecuting(opts.Executing, ExecutionEvent{
						Phase: "confirming",
						// The confirmed kill's own target, not the
						// window head: a window spans many targets
						// and the walk crosses them, so a head-pinned
						// index reads as a stuck campaign while the
						// symbol changes underneath it.
						TargetIndex: dispatchedTargets + wi + 1, TargetCount: int(preparedTargets.Load()),
						Symbol:         targets[window[wi].target].Symbol,
						CandidatesDone: int(mutantsDone.Load()), CandidatesTotal: int(preparedCandidates.Load()),
						ConfirmationsDone: confirmDone, ConfirmationsTotal: confirmTotal,
						ConfirmationMode: confirmMode,
					})
					initialKiller := killers[wi][k.mi]
					outcome, killer, err := rescore(wi, k.mi, func(w work, m engine.Mutant) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, error) {
						return t.confirmMutant(ctx, w, m, initialKiller, scopedBaselines, opts, runEnv)
					})
					if err != nil {
						return confirmInconclusive, err
					}
					confirmDone++
					classified := classifyConfirmation(outcome, killer)
					if classified == confirmFlipped {
						if flips[wi] == nil {
							flips[wi] = map[int]string{}
						}
						flips[wi][k.mi] = initialKiller
						reportConfirmationFlip(opts.Executing,
							targets[window[wi].target].Symbol,
							window[wi].candidates[k.mi].Position,
							initialKiller,
							dispatchedTargets+wi+1, int(preparedTargets.Load()))
					}
					return classified, nil
				},
				func(k windowKill) bool {
					return observations[k.target][k.mi].Unverifiable
				},
			)
			if err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cancel()
		dispatchedTargets += len(window)
		scores := make([]windowScores, len(window))
		for wi := range window {
			scores[wi] = windowScores{outcomes: outcomes[wi], killers: killers[wi], flips: flips[wi], memoryDecided: memoryDecided[wi]}
		}
		return &executedWindow{window: window, scores: scores, observations: observations, incompletes: incompletes, discard: discard}, nil
	}
	// aggregateWindow folds one executed window into findings and commits
	// each one — always before the next window dispatches
	// (REQ-exec-cancellation's incremental-commit clause): the main loop
	// aggregates window N right after gathering window N+1, so in the
	// common case (preparation running ahead of execution) commits land
	// immediately, and only a preparation-bound campaign holds one
	// window's aggregation until the next window's items arrive — which is
	// what lets the campaign epilogue fire between the LAST window's
	// execution and its aggregation, exactly where it always sat.
	aggregateWindow := func(st *executedWindow) error {
		window, observations, incompletes, scores := st.window, st.observations, st.incompletes, st.scores
		for wi := range window {
			if st.discard != nil && st.discard[wi] {
				// A work the graceful drain discarded whole: nothing
				// committable, the prior record stands, and the
				// interrupt error names the truncation
				// (REQ-exec-cancellation's graceful-interrupt clause).
				targetDone(window[wi].target)
				continue
			}
			w := window[wi]
			if err := ctx.Err(); err != nil {
				return err
			}
			if opts.aggregate != nil {
				opts.aggregate()
			}
			f := &findings[w.target]
			if w.serve != nil {
				spliced, err := t.spliceServedFinding(ctx, runEnv, *w.serve, w.candidates, w.flagged, w.baselines, w.targetView, w.oracleViews, w.currentLedger, scores[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Exemptions, oracleMemoryPin, maxGroupBudget(w, opts), deriveOracleBudgets)
				if err != nil {
					// The splice revalidates recorded and fresh evidence
					// against disk; motion between execution and here is
					// this target's drift, never the campaign's — the
					// same locality the fresh-assembly attach has.
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if !errors.Is(err, errEvidenceFinalization) {
						return err
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				}
				// The aggregated work item's retained observations are dead past
				// this point; releasing them per item keeps the run's peak at the
				// in-flight items rather than the whole campaign.
				observations[wi] = nil
				if unavailable, err := producerValidation(ctx, w.producer); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				} else if unavailable != "" {
					stampUnverifiable(&spliced, unavailable)
					// The splice bucketed its survivors before this
					// record-wide stamp: under it, uncovered by an
					// exemption, every survivor's advisory bucket
					// re-derives as a fresh measure's would
					// (REQ-exec-survivor-evidence).
					if unstableForBuckets(&spliced, opts.Exemptions) {
						if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, coverageCache, 0); err != nil {
							return err
						}
					}
				}
				// Served prefix + re-executed candidates both validated
				// against the current tree; provenance recomputes like a
				// fresh measure's (REQ-result-stale, REQ-result-layers).
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, nil, &spliced); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(w.target, stagedDrift+residue())
					continue
				}
				if spliced.EvidenceFault != "" && unstableForBuckets(&spliced, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, coverageCache, 0); err != nil {
						return err
					}
				}
				// The exemption record is derived after every unverifiable stamp
				// the record can carry, never before one (the exemptions'
				// derived-state rule).
				stampExemptions(&spliced, opts.Exemptions)
				spliced.Run = opts.RunID
				// Evidence beats attestation on the flagged re-execution
				// exactly as on a fresh measure: an attested flagged
				// candidate the re-execution killed contradicts its
				// equivalence claim, with the killer named, before the
				// commit - never left to the merge layer's vaguer
				// no-longer-reported shed (REQ-attest-survivor).
				contradictKilledDispositions(spliced.Symbol, w.serve.Attested, spliced.Survivors, spliced.Kills, opts.Contradiction)
				if err := commitAndAttribute(ctx, spliced, w); err != nil {
					return err
				}
				continue
			}
			if w.extend != nil {
				extended, err := t.spliceExtendedFinding(ctx, runEnv, *w.extend, w.candidates, w.extendFrom, w.baselines, w.targetView, w.oracleViews, w.currentLedger, scores[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Budget, opts.Exemptions, oracleMemoryPin, maxGroupBudget(w, opts), deriveOracleBudgets)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if !errors.Is(err, errEvidenceFinalization) {
						return err
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				}
				// Same release as the served branch: the splice is computed, the
				// per-candidate observations are dead.
				observations[wi] = nil
				if unavailable, err := producerValidation(ctx, w.producer); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				} else if unavailable != "" {
					stampUnverifiable(&extended, unavailable)
					// The record-wide stamp re-derives every survivor's
					// advisory bucket, the prefix's included: unlike the
					// suffix-local divergence stamp the splice classifies
					// itself, this one is the whole record's
					// (REQ-exec-survivor-evidence).
					if unstableForBuckets(&extended, opts.Exemptions) {
						if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, coverageCache, 0); err != nil {
							return err
						}
					}
				}
				// Served prefix + measured suffix both validated against the
				// current tree; provenance recomputes like a fresh measure's
				// (REQ-result-stale, REQ-result-layers).
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, nil, &extended); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(w.target, stagedDrift+residue())
					continue
				}
				// Advisory execution buckets: a verifiable extension's suffix
				// survivors earn theirs from the current probe like any measured
				// run's, while carried prefix survivors keep their recorded
				// buckets verbatim; a divergence-stamped extension's suffix
				// survivors were already classified unstable by the splice, and
				// a validation- or provenance-stamped record's every survivor.
				if extended.EvidenceFault != "" && unstableForBuckets(&extended, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, coverageCache, 0); err != nil {
						return err
					}
				} else if !unstableForBuckets(&extended, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, coverageCache, len(w.extend.Survivors)); err != nil {
						return err
					}
				}
				// The exemption record is derived after every unverifiable stamp
				// the record can carry, never before one (the exemptions'
				// derived-state rule).
				stampExemptions(&extended, opts.Exemptions)
				extended.Run = opts.RunID
				if err := commitAndAttribute(ctx, extended, w); err != nil {
					return err
				}
				continue
			}
			unionCandidates := w.candidates
			unionOutcomes := scores[wi].outcomes
			unionObservations := observations[wi]
			unionIncompletes := incompletes[wi]
			if w.shaped {
				// Shaped candidates run unobserved in the scratch tree:
				// the union spans the baselines alone (observed on the
				// real tree), and no candidate evidence exists — a
				// shaped record with candidate evidence would never
				// serve (REQ-target-structural).
				unionCandidates, unionOutcomes, unionObservations, unionIncompletes = nil, nil, nil, nil
			}
			state, candidateEvidence, err := completedObservationUnion(ctx, t.dir, runEnv, w.baselines, unionCandidates, unionOutcomes, unionObservations, unionIncompletes, nil)
			if err != nil {
				return err
			}
			// Same release as the served branch: the union is computed, the
			// per-candidate observations are dead.
			observations[wi] = nil
			if err := ctx.Err(); err != nil {
				return err
			}
			f.CandidateEvidence = candidateEvidence
			portable := newPortableUnion(state, runEnv)
			// The relative conversion inside the attach revalidates the
			// union against disk, so an input moving between execution
			// and attach surfaces here; that is this target's drift, not
			// the campaign's — the same locality the producer and
			// provenance refusals below have (REQ-exec-attribution).
			if w.shaped {
				// A shaped finding carries oracle evidence only: its
				// subject is the declared shape, pinned by the shape
				// digest in BodyHash, never a resolvable symbol
				// (REQ-target-structural, REQ-target-manual-recipes).
				oracleEvidence, err := attachOracleEvidence(w.oracleViews, portable)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if !errors.Is(err, errEvidenceFinalization) {
						return err
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				}
				f.OracleEvidence = oracleEvidence
			} else {
				targetEvidence, oracleEvidence, err := attachEvidence(w.targetView, w.oracleViews, portable)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if !errors.Is(err, errEvidenceFinalization) {
						return err
					}
					refuseTarget(w.target, err.Error()+residue())
					continue
				}
				f.TargetEvidence = targetEvidence
				f.OracleEvidence = oracleEvidence
				f.CompartmentLedger = compartmentLedgerFromView(w.currentLedger)
			}
			if unavailable, err := producerValidation(ctx, w.producer); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				refuseTarget(w.target, err.Error()+residue())
				continue
			} else if unavailable != "" {
				stampUnverifiable(f, unavailable)
			}
			if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, w.shapedFiles, f); err != nil {
				return err
			} else if stagedDrift != "" {
				refuseTarget(w.target, stagedDrift+residue())
				continue
			}
			f.Run = opts.RunID
			stampExemptions(f, opts.Exemptions)
			// Fresh path: the memory pin IS the entry-resolved pin, so
			// its raise is vacuous by construction, while the budget
			// raise is exactly where a fresh derived record's bound
			// climbs from the skeleton floor to the loosest group budget
			// its verdicts ran under; the same call is load-bearing on
			// the four serve arms.
			*f = mergeScoredFacts(*f, scores[wi], f.OracleMemoryBytes, maxGroupBudget(w, opts), deriveOracleBudgets)
			f.Operators = summarizeOperators(w.candidates, scores[wi].outcomes)
			for _, summary := range f.Operators {
				if err := ctx.Err(); err != nil {
					return err
				}
				f.Discarded += summary.Discarded
				f.Mutants += summary.Killed + summary.Survived
				f.Killed += summary.Killed
			}
			for mi, candidate := range w.candidates {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch scores[wi].outcomes[mi] {
				case engine.MutantSurvived:
					f.Survivors = append(f.Survivors, newSurvivor(candidate, "", scores[wi].flips, mi))
				case engine.MutantKilled:
					// The keystone persisted: every kill names its killer
					// (REQ-core-attributed-kills). Attribution is evidence of
					// the event, never an oracle-independence license.
					f.Kills = append(f.Kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: scores[wi].killers[mi]})
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if w.shaped {
				// Survivor execution buckets are body semantics
				// (coverage of the mutated symbol); a shaped survivor
				// is a vacuous-oracle finding with nothing to bucket.
			} else if err := t.bucketSurvivorExecution(ctx, f, w, opts, runEnv, coverageCache, 0); err != nil {
				return err
			}
			// A re-measure carries prior dispositions exactly when the
			// mutation domain holds - the judged subject (body hash,
			// operator set) unchanged, the same position and operator
			// surviving re-execution, site content unchanged - regardless
			// of moved measurement pins: "judged afresh" is delivered by
			// the re-execution itself, and a disposition whose mutant a
			// test now kills sheds as a contradiction naming the killer
			// (REQ-attest-survivor).
			domainHeld := func(rec Finding) bool {
				if w.shaped {
					// A shaped target's domain is unobservable from its
					// digest: content-independent for import-boundary,
					// and covering only the one rewritten file for the
					// other classes - never the wider surface the
					// oracle analyzes, which is pinned only by the
					// oracle's runtime evidence - and shaped candidates
					// carry no site anchor. So a shaped disposition
					// carries only under the full pin gate
					// (REQ-attest-survivor's shaped clause).
					return mutationDomainHeld(rec, *f) && shapeEqual(rec.Shape, f.Shape) && sameAttestationPins(rec, *f)
				}
				return mutationDomainHeld(rec, *f)
			}
			if rec, ok := prior[targets[w.target].Symbol]; ok && domainHeld(*rec) {
				kept, siteSheds, gone := carryAnchoredAttestations(rec.Attested, f.Survivors)
				f.Attested = append(f.Attested, kept...)
				f.Withdrawn = carryAnchoredWithdrawn(rec.Withdrawn, f.Attested, f.Survivors)
				// A carry across moved pins is an acceptance that outlives
				// the environment it was judged in - reported distinctly
				// at the moment it rides, so it is auditable
				// (REQ-attest-survivor). A pins-held carry restores the
				// record verbatim and stays quiet, as it always has.
				if !sameAttestationPins(*rec, *f) && opts.AttestationCarried != nil {
					for _, attestation := range kept {
						opts.AttestationCarried(AttestationCarry{
							Symbol: f.Symbol, Position: attestation.Position, Derivation: derivationMove(rec.TargetEvidence, f.TargetEvidence),
							Operator: attestation.Operator, Reason: attestation.Reason,
						})
					}
				}
				// Domain identity does NOT imply equal sites: the site
				// window spans raw file lines and can overhang the
				// symbol's closure by one line, so an adjacent-line edit
				// under a forced re-measure reaches this arm - surfaced,
				// never silent (REQ-attest-survivor).
				for _, d := range siteSheds {
					d.Symbol = f.Symbol
					if opts.AttestationSiteShed != nil {
						opts.AttestationSiteShed(d)
					}
				}
				// Evidence beats attestation: an attested mutant the
				// re-execution killed contradicts its equivalence claim -
				// the specific story, with the killer named, told before
				// the commit so the merge layer's vaguer shed defers to it
				// (REQ-attest-survivor).
				contradictKilledDispositions(f.Symbol, gone, f.Survivors, f.Kills, opts.Contradiction)
			}
			if err := commitAndAttribute(ctx, *f, w); err != nil {
				return err
			}
		}
		return nil
	}
	// The window gather is deterministic: items arrive in target order from
	// the serial preparation goroutine, and window boundaries follow the
	// same budget rule as ever — blocking until each next item is prepared
	// keeps membership independent of execution timing, so the
	// window-scoped confirmation flip signal covers the same kills on
	// every run (REQ-exec-attribution).
	var held *executedWindow
	// aggregateHeld folds the previously executed window on the exit
	// paths: measured evidence is never discarded by a later preparation
	// failure — an abort that discards completed measurements is reserved
	// for corrupted orchestration state (REQ-exec-attribution's abort
	// terms) — while a canceled context refuses inside aggregation as
	// ever, cancellation discarding unfinished work whole.
	aggregateHeld := func() error {
		if held == nil {
			return nil
		}
		err := aggregateWindow(held)
		held = nil
		return err
	}
	// Window EXECUTION order is value-ordered (REQ-exec-run-status's
	// estimate class): the gathering goroutine runs the SAME blocking
	// gather rule as ever — membership stays deterministic and
	// independent of execution timing (REQ-exec-attribution) — and the
	// driver picks the cheapest READY window by the pre-probe
	// projection, so an interrupt at any point has banked the most
	// verdicts its elapsed time could buy. The channel is sized for
	// every possible window, so the gatherer never blocks behind
	// execution and the ready pool deepens while long windows run.
	windowCh := make(chan []work, len(targets))
	go func() {
		defer close(windowCh)
		for {
			window, ok := gatherWindow(items, windowBudget, windowMinimum)
			if !ok {
				return
			}
			windowCh <- window
		}
	}()
	mkReady := func(window []work) readyWindow {
		cost, priced := windowPrice(window, opts.baselineDur)
		return readyWindow{window: window, cost: cost, priced: priced}
	}
	var pool []readyWindow
	windowChOpen := true
	for {
		if len(pool) == 0 {
			if !windowChOpen {
				break
			}
			window, ok := <-windowCh
			if !ok {
				break
			}
			pool = append(pool, mkReady(window))
		}
		if windowChOpen {
			if seams.waitPreparedBeforePick {
				// Test seam: hold each pick until preparation
				// completes, so ordering assertions see the whole
				// pool instead of racing the serial preparation.
				<-prepDone
			}
			select {
			case <-prepDone:
				// Preparation is complete, so every remaining window
				// is imminent (the gatherer only drains the buffered
				// items and closes): drain to closure so the pick sees
				// the WHOLE remaining pool — without this, a window in
				// flight between the items buffer and windowCh loses
				// the pick by a scheduling race.
				for window := range windowCh {
					pool = append(pool, mkReady(window))
				}
				windowChOpen = false
			default:
			drainReady:
				for {
					select {
					case window, ok := <-windowCh:
						if !ok {
							windowChOpen = false
							break drainReady
						}
						pool = append(pool, mkReady(window))
					default:
						break drainReady
					}
				}
			}
		}
		// A failed preparation surfaces before the next window executes,
		// not after every buffered item is paid for: gathered-but-unmeasured
		// items are just preparation, dropped without loss.
		select {
		case <-prepDone:
			if prepErr != nil {
				if err := aggregateHeld(); err != nil {
					return nil, err
				}
				return nil, prepErr
			}
		default:
		}
		pick := nextReadyWindow(pool)
		window := pool[pick].window
		pool = append(pool[:pick], pool[pick+1:]...)
		// The previously executed window aggregates only now, with the
		// next window in hand proving it was not the last: its commits
		// still land before this window dispatches
		// (REQ-exec-cancellation's incremental-commit clause).
		if err := aggregateHeld(); err != nil {
			return nil, err
		}
		executed, err := executeWindow(window)
		if err != nil {
			return nil, err
		}
		if executed.discard != nil {
			// The drained window aggregates NOW — its capped prefixes
			// commit under the incremental-commit clause — and the run
			// ends with the operational interrupt error; later windows
			// never dispatch (REQ-exec-cancellation's graceful-interrupt
			// clause).
			if err := aggregateWindow(executed); err != nil {
				return nil, err
			}
			return nil, ErrInterrupted
		}
		held = executed
	}
	<-prepDone
	if prepErr != nil {
		if err := aggregateHeld(); err != nil {
			return nil, err
		}
		return nil, prepErr
	}
	// The campaign epilogue fires between the last window's execution and
	// its aggregation, exactly where it always sat: preparation and every
	// mutant are done, the last findings fold has not happened, so a
	// transient global drift no surviving target's evidence reflects is
	// reported rather than silently absorbed (REQ-exec-quiescence), and an
	// edit landing at this seam is refused by the last window's own
	// per-target validations.
	if opts.afterExecution != nil {
		opts.afterExecution()
	}
	if err := eachMode(func(mv *modeViews) error {
		return mv.views.validateProducers(ctx)
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		treeDrift = err
	}
	if held != nil {
		if err := aggregateWindow(held); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A run that returns success has dispositioned every announced
	// target: measured and committed, served, skipped, or named in the
	// drift error. There is no fourth silent outcome - a pipeline that
	// ended early with a lost error would otherwise report a truncated
	// campaign as complete, indistinguishable from success unless the
	// operator diffs the document against the roster
	// (REQ-exec-completion).
	driftedSymbols := make(map[string]bool, len(drifted))
	for _, d := range drifted {
		driftedSymbols[d.Symbol] = true
	}
	var unfinished []string
	for i := range findings {
		if findings[i].Cached || findings[i].Skipped != "" || committed[i] || driftedSymbols[targets[i].Symbol] {
			continue
		}
		unfinished = append(unfinished, targets[i].Symbol)
	}
	if len(unfinished) > 0 {
		return nil, fmt.Errorf("gomutant: campaign truncated - %s ended without a result and without an error; the findings document keeps each unfinished target's prior record", cappedNameList(unfinished, "target"))
	}
	if opts.Posture != nil {
		// Every completed record's reuse posture, judged over the views
		// this run built (the inspection pass's chain without its view
		// builds), keyed by the record's posture; a judgment fault
		// is that record's posture, never the run's error, and a
		// cancellation here stops the postures, never the finished run.
		shared := t.newAdmissionShared()
		for i := range findings {
			if ctx.Err() != nil {
				break
			}
			f := findings[i]
			if driftedSymbols[targets[i].Symbol] {
				continue
			}
			if f.Skipped != "" {
				opts.Posture(RecordPosture{Symbol: f.Symbol, Measurement: "skipped"})
				continue
			}
			// The record's own posture, decided at its admission, keys
			// the set, exactly as the inspection pass keys its prebuilt
			// views: the set's engine carries the package-process
			// execution posture, which changes what the evidence check
			// means, so a set built under the other posture is never
			// read even when it holds the symbol; a posture the run built
			// no set under is judged over a supplementary view, as the
			// inspection pass would.
			var inspection FindingInspection
			adm, err := t.admitFindingContext(ctx, f, shared)
			if err == nil {
				var prebuilt *subjectViewSet
				if mv, ok := modes[adm.packageProcess]; ok {
					prebuilt = mv.views
				}
				inspection, err = t.judgeAdmittedContext(ctx, f, adm, prebuilt)
			}
			if err == nil {
				inspection = withCandidateEvidence(inspection, f)
			}
			if ctx.Err() != nil {
				break
			}
			opts.Posture(posture(f, inspection, err))
		}
	}
	if len(drifted) > 0 || treeDrift != nil {
		completed := completedFindings(findings, drifted)
		if len(drifted) == 0 {
			return completed, &TreeDriftError{Completed: len(completed), Transient: treeDrift.Error()}
		}
		return completed, &TreeDriftError{Drifted: drifted, Completed: len(completed)}
	}
	return findings, nil
}

// ErrInterrupted is the graceful-interrupt run result: an operational
// cancellation — the campaign did not finish — whose drained prefixes
// are already committed as candidate-capped records; re-running the
// same command serves the committed work and measures the remainder.
// Exported so faces and embedders can classify the one outcome the
// graceful drain exists to distinguish from a hard abort
// (REQ-exec-cancellation's graceful-interrupt clause).
var ErrInterrupted = errors.New("gomutant: interrupted - in-flight mutants drained and measured prefixes committed as candidate-capped records; re-run the same command to measure the remainder")
