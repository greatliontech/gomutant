package gomutant

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

var findingObservationSequence atomic.Uint64

// lockCallbacks wraps every caller callback in one mutex so pipelined
// preparation and execution — which invoke callbacks from two goroutines —
// preserve the synchronous-caller-code contract: no two callbacks ever run
// concurrently (REQ-exec-run-status). AnalysisProgress is exempt by its own
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
	return opts
}

// Options bound a run.
type Options struct {
	// Budget caps selected candidates per symbol; 0 means all (REQ-mut-budget).
	Budget int
	// OracleTimeout bounds each oracle process; 0 means 60s.
	OracleTimeout time.Duration
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
	// reads — module-relative paths (a file or a directory tree) or
	// absolute files — extending each spawn's observation bracket beyond
	// the oracle package directory (REQ-exec-observation). An absolute
	// external directory cannot be walked and is refused at run start,
	// as is a path under a tool-excluded directory. Declaring a path
	// carries the bracket contract's mutation-free assertion for the
	// span.
	BracketPaths []string
	// Staged pins the run to the git index snapshot
	// (REQ-result-staged): staged-but-uncommitted content is the
	// measured subject and counts clean; unstaged drift over a
	// measured package's inputs refuses that target instead of
	// stamping dirty machine-local evidence, and the finding records
	// the index tree identity the eventual commit will carry.
	Staged bool
	// Exemptions is the committed exemption record
	// (REQ-result-exemptions): reviewed acceptances of exactly-named
	// unverifiable reasons per subject, consumed by survivor bucketing
	// and stamped onto each finding they cover; the persistence layer
	// re-reads the record itself, so revocation bites every later
	// classification.
	Exemptions []Exemption
	// ScratchNamespaces declares in-module run-scratch namespaces for
	// observation ingest (REQ-exec-scratch-namespace): each is a
	// module-relative directory plus a single-component os.MkdirTemp
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
	// AnalysisProgress must be safe for concurrent invocation and synchronously receives advisory keep-alive events from
	// the run's freshness analysis — the gofresh phase name and, for
	// per-package phases, the package. Events are diagnostic, carry no
	// completion signal, never enter a decision or finding, and their sequence
	// is not part of the deterministic run-status contract
	// (REQ-exec-run-status). The callback must return normally.
	AnalysisProgress func(phase, pkg string)
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
	// ("", 1) before the shared union pass, then (symbol, 2) before a
	// faulted target's bounded per-target retry — a test seam pinning
	// the union/retry split, like producer above.
	proofAttempt func(symbol string, attempt int)
	// Contradiction receives each attested survivor a growth serve's added
	// tests killed: the attestation is shed — evidence beats attestation —
	// and the contradiction is worth a human's attention, because a mutant
	// judged equivalent was just distinguished (REQ-attest-survivor,
	// REQ-result-stale's growth carve-out). Nil drops the reports; the
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
	// flagged-only dispatch under a candidate-evidence serve, and
	// survivor-only dispatch under a growth serve, like producer above.
	dispatched func(symbol string, mi int)
}

// PreparationStage identifies one observable pre-execution operation.
type PreparationStage string

const (
	PreparationLoading   PreparationStage = "loading"
	PreparationResolving PreparationStage = "resolving"
	PreparationFreshness PreparationStage = "freshness"
	PreparationMutants   PreparationStage = "mutants"
	PreparationBaseline  PreparationStage = "baseline"
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
	// silent — a false-survivor field report is self-diagnosing from
	// the log (docs/issues/growth-serve-misses-modified-oracle-bodies.md).
	FlipPosition string `json:"flipPosition,omitempty"`
	FlipKiller   string `json:"flipKiller,omitempty"`
}

// PreparationEvent reports one operation before it begins. Symbol is set for
// target-scoped operations; Package is additionally set for baseline probes.
type PreparationEvent struct {
	Stage   PreparationStage `json:"stage"`
	Symbol  string           `json:"symbol,omitempty"`
	Package string           `json:"package,omitempty"`
}

// RunDecision explains whether one target is cached, skipped, or measured.
type RunDecision struct {
	Symbol     string `json:"symbol"`
	Action     string `json:"action"`
	Reason     string `json:"reason,omitempty"`
	Candidates int    `json:"candidates,omitempty"`
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
	Targets   int `json:"targets"`
	Measured  int `json:"measured"`
	Cached    int `json:"cached"`
	Skipped   int `json:"skipped"`
	Generated int `json:"generated"`
	Discarded int `json:"discarded"`
	Killed    int `json:"killed"`
	Survived  int `json:"survived"`
	Attested  int `json:"attested"`
	Open      int `json:"open"`
}

// SummarizeRun derives deterministic aggregate totals from findings.
func SummarizeRun(findings []Finding) RunSummary {
	summary := RunSummary{Targets: len(findings)}
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
	return summary
}

// group is one test-binary invocation: a package, its oracle's -run
// pattern, and the binary's flags.
type group struct {
	pkgs                  []string
	runRegex              string
	flags                 []string
	moduleDir, packageDir string
}

type packageContextResult struct {
	moduleDir, packageDir string
	err                   error
}

type oracleValidationResult struct {
	err error
}

type runPreparation struct {
	packageOf      func(context.Context, string) (string, string, error)
	testsOf        func(context.Context, string) ([]string, error)
	validate       func(context.Context, []string) error
	contextFor     func(context.Context, string) (string, string, error)
	splitRapidPkgs func(context.Context, []string) ([]string, []string, error)
	runtimesOf     func(context.Context, []string) (map[string][]string, error)

	verifyEnumeration func(context.Context, string, []string) error
	derivedOracles    map[string][]string
	validations       map[string]oracleValidationResult
	contexts          map[string]packageContextResult
	rapid             map[string]bool
	runtimes          map[string][]string
	notedProperty     map[string]bool
}

func newRunPreparation(t *Tree) *runPreparation {
	return &runPreparation{
		packageOf:         t.eng.PackageOfContext,
		testsOf:           t.eng.TestsOfContext,
		validate:          t.eng.ValidateOracleContext,
		contextFor:        t.eng.PackageContextContext,
		splitRapidPkgs:    t.eng.SplitRapidPkgsContext,
		runtimesOf:        t.eng.PropertyRuntimesContext,
		verifyEnumeration: t.eng.VerifyTestEnumerationContext,
		derivedOracles:    map[string][]string{},
		validations:       map[string]oracleValidationResult{},
		contexts:          map[string]packageContextResult{},
	}
}

func (p *runPreparation) oracle(ctx context.Context, target Target) ([]string, error) {
	if len(target.Oracle) > 0 || target.OracleExplicit {
		return slices.Clone(target.Oracle), ctx.Err()
	}
	pkg, _, err := p.packageOf(ctx, target.Symbol)
	if err != nil {
		return nil, err
	}
	if pkg == "" {
		return nil, nil
	}
	if oracle, ok := p.derivedOracles[pkg]; ok {
		return slices.Clone(oracle), ctx.Err()
	}
	oracle, err := p.testsOf(ctx, pkg)
	if err != nil {
		return nil, err
	}
	// The derived set is an oracle pin only if it is provably fresh: the
	// package loader's snapshot has been observed lagging the filesystem, so
	// the enumeration is cross-checked against a direct parse of the
	// package's on-disk test files before it is trusted — once per package,
	// memoized with the set it vouches for.
	if p.verifyEnumeration != nil {
		if err := p.verifyEnumeration(ctx, pkg, oracle); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.derivedOracles[pkg] = slices.Clone(oracle)
	return oracle, nil
}

func (p *runPreparation) validateOracle(ctx context.Context, oracle []string) error {
	key := sequenceKey(oracle)
	if result, ok := p.validations[key]; ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return result.err
	}
	err := p.validate(ctx, oracle)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return cancelErr
	}
	p.validations[key] = oracleValidationResult{err: err}
	return err
}

func (p *runPreparation) packageContext(ctx context.Context, pkg string) (string, string, error) {
	if result, ok := p.contexts[pkg]; ok {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		return result.moduleDir, result.packageDir, result.err
	}
	moduleDir, packageDir, err := p.contextFor(ctx, pkg)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return "", "", cancelErr
	}
	p.contexts[pkg] = packageContextResult{moduleDir: moduleDir, packageDir: packageDir, err: err}
	return moduleDir, packageDir, err
}

func (p *runPreparation) rapidPackages(ctx context.Context, candidates []string) (map[string]bool, error) {
	if p.rapid != nil {
		return p.rapid, ctx.Err()
	}
	rapid, _, err := p.splitRapidPkgs(ctx, candidates)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.rapid = map[string]bool{}
	for _, pkg := range rapid {
		p.rapid[pkg] = true
	}
	return p.rapid, nil
}

// noteProperty reports whether pkg's prerequisite statement has not yet
// been emitted this run and marks it emitted - one statement per
// package per run.
func (p *runPreparation) noteProperty(pkg string) bool {
	if p.notedProperty == nil {
		p.notedProperty = map[string]bool{}
	}
	if p.notedProperty[pkg] {
		return false
	}
	p.notedProperty[pkg] = true
	return true
}

// propertyRuntimes memoizes the oracle packages' recognized property
// runtimes for the run's prerequisite statements
// (REQ-exec-property-oracles).
func (p *runPreparation) propertyRuntimes(ctx context.Context, candidates []string) (map[string][]string, error) {
	if p.runtimes != nil {
		return p.runtimes, ctx.Err()
	}
	runtimes, err := p.runtimesOf(ctx, candidates)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.runtimes = runtimes
	if p.runtimes == nil {
		p.runtimes = map[string][]string{}
	}
	return p.runtimes, nil
}

func sequenceKey(values []string) string {
	var key strings.Builder
	for _, value := range values {
		fmt.Fprintf(&key, "%d:", len(value))
		key.WriteString(value)
	}
	return key.String()
}

// Run mutates each target and executes its oracle per mutant, fanning
// mutant runs across a worker pool (REQ-exec-oracle-run). Prior findings
// are served only when every target and oracle evidence record, operator,
// oracle-timeout and budget pins hold, unless forced (REQ-result-stale). A run that
// cannot attribute an outcome aborts without findings
// (REQ-core-attributed-kills).
// probeOracleInstability attributes unverifiable runtime evidence under
// a package-derived oracle by probing each oracle test alone: a test
// whose solo baseline run produces unverifiable evidence is the
// instability's producer; a clean sweep means no single test reproduces
// it (REQ-exec-oracle-guidance). Probes are best-effort - a probe that
// errors, matches nothing, or fails skips its test rather than
// aborting the run whose finding already committed.
func (t *Tree) probeOracleInstability(ctx context.Context, oracle []string, groups []group, opts Options, runEnv []string) (oracleAttribution, error) {
	byPkg := make(map[string]group, len(groups))
	for _, g := range groups {
		byPkg[g.pkgs[0]] = g
	}
	var attr oracleAttribution
	for _, test := range oracle {
		if err := ctx.Err(); err != nil {
			return oracleAttribution{}, err
		}
		pkg, fn := splitTestSymbol(test)
		g, ok := byPkg[pkg]
		if pkg == "" || fn == "" || !ok {
			continue
		}
		_, passed, _, observed, err := engine.TestProbeObservedEnv(ctx, t.dir, pkg, "^"+regexp.QuoteMeta(fn)+"$", opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
		if err != nil {
			if ctx.Err() != nil {
				return oracleAttribution{}, ctx.Err()
			}
			if attr.firstErr == "" {
				attr.firstErr = err.Error()
			}
			continue
		}
		attr.completed++
		if state, serr := runtimeinput.CompletedState(observed); serr == nil {
			if paths, pok := moduleRelInputs(state.Manifest, t.dir); pok {
				for _, rel := range paths {
					if attr.probedPaths == nil {
						attr.probedPaths = map[string]bool{}
					}
					attr.probedPaths[rel] = true
				}
			}
		}
		if passed && observed.Unverifiable {
			attr.unstable = append(attr.unstable, test)
		}
	}
	return attr, nil
}

// contradictKilledDispositions reports each prior disposition whose
// mutant the current outcomes killed — evidence beats attestation, the
// killer named from the durable keystone (REQ-attest-survivor). A
// disposition whose mutant still survives, or is absent from the kill
// ledger (vanished, de-selected), is left to the carry and merge layers.
// A served record whose kill ledger is incomplete (predating kill
// attribution) is deliberately in the second class: with no ledger, a
// killed disposition cannot be told from a vanished one, and an
// attribution-less contradiction would misfire on the vanished — the
// merge layer's loud no-longer-reported shed covers both honestly.
func contradictKilledDispositions(symbol string, prior []Attestation, survivors []Survivor, kills []Kill, emit func(AttestationContradiction)) {
	if emit == nil || len(prior) == 0 {
		return
	}
	surviving := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		surviving[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	killerOf := make(map[survivorKey]string, len(kills))
	for _, kill := range kills {
		killerOf[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	for _, attestation := range prior {
		key := survivorKey{attestation.Position, attestation.Operator}
		if surviving[key] {
			continue
		}
		if killer, killed := killerOf[key]; killed {
			emit(AttestationContradiction{
				Symbol: symbol, Position: attestation.Position,
				Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
			})
		}
	}
}

// AttestationCarry reports one disposition carried across moved
// measurement pins under a held mutation domain (REQ-attest-survivor).
type AttestationCarry struct {
	Symbol   string
	Position string
	Operator string
	Reason   string
}

// AttestationContradiction reports one attested survivor a growth serve's
// added tests killed, with the shed attestation's reasoning and the killer
// so the re-judgment starts from the evidence.
type AttestationContradiction struct {
	Symbol   string
	Position string
	Operator string
	Killer   string
	Reason   string
}

// work is one target's resolved measurement state across the run's
// three phases.
type work struct {
	target int
	oracle []string
	reason string
	// shaped marks a shaped target's work: no target view, no
	// compartment ledger, wholesale serve-or-remeasure, and survivor
	// buckets stay unclassified (coverage is body semantics).
	shaped      bool
	candidates  []engine.Candidate
	groups      []group
	oracleSet   map[string]bool
	targetView  *subjectView
	oracleViews []*subjectView
	producer    *subjectViewSet
	// currentLedger is the target package's compartment ledger, read from
	// the view's observation at preparation time: the value is fixed at
	// view build (every sibling narrowing serves the union's one
	// observation), so one read here keeps phase three free of
	// per-finding view reads.
	currentLedger gofresh.TestVariantLedger
	baselines     []runtimeinput.Observation
	// serve is the prior record being served with candidate-local
	// re-execution (REQ-result-stale): only the candidate indexes in
	// flagged execute, and phase three splices their fresh outcomes and
	// evidence into the served record.
	serve   *Finding
	flagged map[int]bool
	// extend is the prior capped record whose measured prefix stands under
	// a wider budget request (REQ-mut-budget, REQ-result-stale's
	// budget-extension carve-out): candidates below extendFrom keep their
	// recorded outcomes, only the unmeasured suffix executes, and phase
	// three splices the suffix outcomes onto the record.
	extend     *Finding
	extendFrom int
	// grow is the prior record served under the oracle-growth carve-out
	// (REQ-result-stale): the derived oracle grew by growAdded while the
	// compartment delta classified inert, so only the recorded survivors
	// (growSurvivors, by candidate index) re-execute — against oracle
	// groups built over the added tests alone — and phase three splices
	// their fresh outcomes into the record under the current tree's
	// evidence.
	grow          *Finding
	growSurvivors map[int]bool
	growAdded     []string
	// drift is the prior record served under the killer-drift carve-out
	// (REQ-result-stale): the target package's compartment moved by an
	// attributable delta, driftMoved names the oracles whose evidence or
	// reference walk observed it, and only the candidate indexes in
	// driftRemeasure — every survivor when the set grew or anything moved
	// (driftAdded names the current derived oracles with no recorded
	// evidence: the grown-set composition, whose added tests join the
	// re-measure oracle and cannot un-kill a standing kill), every
	// candidate carrying recorded candidate evidence, every kill whose
	// killer moved, and
	// every timeout or package-scope kill when anything moved — re-execute
	// against the full current oracle; kills keyed to unmoved oracles and
	// unflagged discards keep their recorded dispositions, and phase three
	// splices the fresh outcomes into the record under the current tree's
	// evidence.
	drift          *Finding
	driftMoved     []string
	driftAdded     []string
	driftRemeasure map[int]bool
}

// executeWorkMutant dispatches one work item's mutant to its executor:
// shaped candidates materialize in a scratch tree (a structural oracle
// analyzes source at runtime, so the forbidden state must exist on
// disk — the overlay reaches only the oracle binary's own
// compilation), body mutants run through the build overlay.
func (t *Tree) executeWorkMutant(ctx context.Context, w work, m engine.Mutant, opts Options, runEnv []string) (engine.MutantOutcome, string, runtimeinput.Observation, string, error) {
	if w.shaped {
		outcome, killer, err := t.executeShapedCandidate(ctx, w, m, opts, runEnv)
		return outcome, killer, runtimeinput.Observation{}, "", err
	}
	return t.executeMutant(ctx, w, m, opts, runEnv)
}

// confirmMutant is the serial kill confirmation's execution
// (REQ-exec-attribution): a test-attributed kill runs KILLER-SCOPED
// first — the killing test alone, same oracle bounds — and an
// attributed test kill from that run is the scored measurement,
// admitted only over a passing KILLER-SCOPED BASELINE of the
// unmutated tree: differential attribution needs the vouching pass
// to share the mutant run's shape (REQ-exec-attribution-symmetry's
// "differ in the overlay alone", here on the run-regex axis — a
// killer that fails standalone regardless of the mutant, a
// prefix-dependent setup ordering, must never convert a
// sibling-induced false kill into a confirmed one). The scoped
// baseline memoizes per (group, killer) across the campaign — one
// extra one-test probe per distinct killer, not per confirmation.
// Anything weaker than an attributed scoped kill over a passing
// scoped baseline — the killer passing, a timeout either side, a
// discard, an unscopable killer — falls back to the full serial
// oracle, whose verdict scores: a survivor verdict always rests on
// the whole oracle.
func (t *Tree) confirmMutant(ctx context.Context, w work, m engine.Mutant, killer string, scopedBaselines map[scopedBaselineKey]bool, opts Options, runEnv []string) (engine.MutantOutcome, string, runtimeinput.Observation, string, error) {
	scopedGroup := func() *group {
		if w.shaped || killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) {
			return nil
		}
		pkg, fn := splitTestSymbol(killer)
		if pkg == "" || fn == "" {
			return nil
		}
		// Defensive: the engine's attribution already truncates a
		// subtest to its top-level function; a slash here would still
		// scope soundly to the parent (which runs its children).
		if i := strings.IndexByte(fn, '/'); i > 0 {
			fn = fn[:i]
		}
		for i := range w.groups {
			g := &w.groups[i]
			// Group constructors build single-package groups; the
			// length check is defensive against a future multi-pkg
			// shape, which would fall back to the full oracle.
			if len(g.pkgs) == 1 && g.pkgs[0] == pkg {
				scoped := *g
				scoped.runRegex = "^" + regexp.QuoteMeta(fn) + "$"
				return &scoped
			}
		}
		return nil
	}()
	if scopedGroup != nil && t.scopedBaselinePasses(ctx, *scopedGroup, scopedBaselines, opts, runEnv) {
		out, gk, state, incomplete, diagnostic, err := engine.RunMutantObservedEnv(ctx, t.dir, m, scopedGroup.pkgs, scopedGroup.runRegex, opts.OracleTimeout, scopedGroup.flags, scopedGroup.moduleDir, scopedGroup.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
		if err != nil {
			return out, gk, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: killer-scoped confirmation: %w", m.Symbol, m.Position, m.Operator, err)
		}
		if diagnostic == "" && out == engine.MutantKilled && gk != TimeoutKiller && !strings.HasPrefix(gk, PackageKillerPrefix) {
			if aerr := attributedKill(gk, w.oracleSet); aerr != nil {
				return out, gk, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, aerr)
			}
			merged, err := mergeFindingObservationsContext(ctx, t.dir, runEnv, state)
			if err != nil {
				return out, gk, runtimeinput.Observation{}, "", fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
			}
			return out, gk, merged, incomplete, nil
		}
	}
	return t.executeWorkMutant(ctx, w, m, opts, runEnv)
}

// scopedBaselineKey identifies one killer-scoped baseline probe: the
// memo's identity is the probe's whole shape.
type scopedBaselineKey struct {
	pkg, run, flags, moduleDir, packageDir string
}

// scopedBaselinePasses reports whether the killing test passes ALONE
// on the unmutated tree — the killer-scoped stage's differential
// ground — memoized in the caller's Run-local map (this Run's probe
// gate serializes every confirmation, so the memo needs no lock, and
// Run-locality is what keeps the key complete across bounds). Any
// non-pass — a standalone failure, a timeout, a probe error —
// answers false and the confirmation falls back to the full oracle;
// the probe is best-effort ground, never a campaign abort.
func (t *Tree) scopedBaselinePasses(ctx context.Context, g group, memo map[scopedBaselineKey]bool, opts Options, runEnv []string) bool {
	key := scopedBaselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
	if passed, ok := memo[key]; ok {
		return passed
	}
	ran, passed, _, _, err := engine.TestProbeObservedEnv(ctx, t.dir, g.pkgs[0], g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
	ok := err == nil && ran > 0 && passed
	memo[key] = ok
	return ok
}

// executeMutant runs one mutant through its oracle groups and merges
// the process observations - the shared execution the worker pool and
// the serial kill confirmation both use.
func (t *Tree) executeMutant(ctx context.Context, w work, m engine.Mutant, opts Options, runEnv []string) (engine.MutantOutcome, string, runtimeinput.Observation, string, error) {
	outcome := engine.MutantSurvived
	killer := ""
	incompleteReason := ""
	var processStates []runtimeinput.Observation
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return outcome, killer, runtimeinput.Observation{}, "", err
		}
		if outcome != engine.MutantSurvived {
			break
		}
		out, groupKiller, state, incomplete, diagnostic, err := engine.RunMutantObservedEnv(ctx, t.dir, m, g.pkgs, g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
		if diagnostic != "" {
			// The mutant failed this group's build: no test process
			// started, so the group contributes no runtime observation and
			// no incomplete-process evidence — the discard is a pure
			// function of the mutant source under the toolchain and
			// build-configuration pins the serve validates
			// (REQ-result-stale). Groups that ran keep their observations.
			outcome = out
			killer = groupKiller
			if err != nil {
				return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
			}
			continue
		}
		processStates = append(processStates, state)
		if incompleteReason == "" {
			incompleteReason = incomplete
		}
		if err == nil && out == engine.MutantKilled {
			err = attributedKill(groupKiller, w.oracleSet)
		}
		if err != nil {
			return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
		}
		outcome = out
		killer = groupKiller
	}
	if err := ctx.Err(); err != nil {
		return outcome, killer, runtimeinput.Observation{}, "", err
	}
	state, err := mergeFindingObservationsContext(ctx, t.dir, runEnv, processStates...)
	if err != nil {
		return outcome, killer, runtimeinput.Observation{}, "", fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
	}
	return outcome, killer, state, incompleteReason, nil
}

// bucketSurvivorExecution classifies why each survivor lived
// (REQ-exec-survivor-evidence): unverifiable runtime evidence buckets
// every survivor "unstable-oracle" without probing; otherwise one
// baseline coverage probe per oracle group (cached across the run's
// targets sharing a group and cover package) decides "never-executed"
// versus "executed-and-passed" by whether any executed block spans the
// mutated position. Advisory classification on the unmutated tree,
// never a measurement pin. Survivors below from keep their recorded
// buckets verbatim: a splice carries its served prefix's advisory data —
// measured under the record's own verifiable conditions — like its
// dispositions and attestations, immune to a suffix-local divergence
// stamp or re-probe.
func (t *Tree) bucketSurvivorExecution(ctx context.Context, f *Finding, w work, opts Options, runEnv []string, cache map[string]engine.Coverage, from int) error {
	if from >= len(f.Survivors) {
		return nil
	}
	if unstableForBuckets(f, opts.Exemptions) {
		for si := from; si < len(f.Survivors); si++ {
			f.Survivors[si].Execution = "unstable-oracle"
		}
		return nil
	}
	// The mutant executes through the build overlay, so an oracle whose
	// observed union read any mutated file's on-disk bytes derived its
	// verdict from the unmutated tree - the false-survivor channel a
	// disk-walking oracle opens. The union read is target-scoped truth:
	// every re-measured survivor buckets overlay-bypassed, the
	// labeled-never-silent direction (REQ-exec-survivor-evidence).
	if overlayBypassedRead(f, w) {
		for si := from; si < len(f.Survivors); si++ {
			f.Survivors[si].Execution = "overlay-bypassed"
		}
		return nil
	}
	coverage, probed, err := t.oracleCoverage(ctx, w, opts, runEnv, cache)
	if err != nil {
		return err
	}
	if !probed {
		// Best-effort: an unprobeable oracle leaves the bucket empty
		// rather than failing a run whose measurement is already sound.
		return nil
	}
	coverPkg := w.targetView.subject.Package
	for si := from; si < len(f.Survivors); si++ {
		covered, ok := survivorCovered(coverage, coverPkg, f.Survivors[si])
		if !ok {
			// An unparseable position leaves the bucket UNSET — the
			// best-effort posture — never a claimed never-executed.
			continue
		}
		if covered {
			f.Survivors[si].Execution = "executed-and-passed"
		} else {
			f.Survivors[si].Execution = "never-executed"
		}
	}
	return nil
}

// survivorCovered is the one coverage question every bucket decision
// asks — fresh classification and the growth upgrade pass alike: does
// any executed block intersect the mutated node's extent? A point
// anchor sits on toolchain-dependent block boundaries (go1.27 moved
// body spans off the brace token), so the range is the probe;
// extent-less records — prior generations — keep the anchor-point
// form they were bucketed under.
func survivorCovered(coverage engine.Coverage, coverPkg string, s Survivor) (covered, ok bool) {
	file, line, col, posOK := splitSurvivorPosition(s.Position)
	if !posOK {
		// Not a coverage verdict: the caller owns unparseable-position
		// policy (fresh classification leaves the bucket unset; the
		// growth upgrade leaves the recorded bucket standing).
		return false, false
	}
	if sl, sc, el, ec, extOK := parseSurvivorExtent(s.Extent); extOK {
		return coverage.Intersects(coverPkg+"/"+file, sl, sc, el, ec), true
	}
	return coverage.Covered(coverPkg+"/"+file, line, col), true
}

// parseSurvivorExtent splits "line:col-line:col"; ok is false on the
// empty extents of prior-generation records. A carried survivor may
// pair a current candidate's extent with a bucket recorded under the
// point probe — the extent describes the mutant, never which probe
// produced the bucket.
func parseSurvivorExtent(extent string) (startLine, startCol, endLine, endCol int, ok bool) {
	if extent == "" {
		return 0, 0, 0, 0, false
	}
	if n, err := fmt.Sscanf(extent, "%d:%d-%d:%d", &startLine, &startCol, &endLine, &endCol); err != nil || n != 4 {
		return 0, 0, 0, 0, false
	}
	if fmt.Sprintf("%d:%d-%d:%d", startLine, startCol, endLine, endCol) != extent {
		// Sscanf tolerates trailing bytes; a round-trip mismatch is a
		// malformed extent, and malformed means the point fallback.
		return 0, 0, 0, 0, false
	}
	return startLine, startCol, endLine, endCol, true
}

// coverageUpgradeAllowed reports whether a coverage-probe re-derivation
// may overwrite a survivor's recorded execution bucket: only empty and
// never-executed buckets upgrade. overlay-bypassed and unstable-oracle
// were judged from evidence a coverage probe cannot see - the observed
// union's disk reads and the runtime-evidence verdict - so a probe
// never overrides them, and executed-and-passed is already the
// probe's own ceiling (REQ-exec-survivor-evidence).
func coverageUpgradeAllowed(execution string) bool {
	return execution == "" || execution == "never-executed"
}

// overlayBypassedRead reports whether the finding's observed union
// recorded a read of any mutated file's own path: the replacements'
// catalog paths and the finalized manifest identities are both
// absolute, so the comparison is exact. Best-effort advisory - an
// unreadable manifest leaves the coverage buckets to speak.
func overlayBypassedRead(f *Finding, w work) bool {
	if f.TargetEvidence.RuntimeInputs == "" || w.targetView == nil {
		return false
	}
	mutated := map[string]bool{}
	for _, candidate := range w.candidates {
		for _, replacement := range candidate.Replacements {
			mutated[replacement.File] = true
		}
	}
	if len(mutated) == 0 {
		return false
	}
	paths, err := runtimeinput.Paths(f.TargetEvidence.RuntimeInputs, w.targetView.moduleDir)
	if err != nil {
		return false
	}
	for _, path := range paths {
		if mutated[path] {
			return true
		}
	}
	return false
}

// splitSurvivorPosition splits "file.go:line:col" (an occurrence suffix
// "#n" stripped from the column).
func splitSurvivorPosition(position string) (file string, line, col int, ok bool) {
	parts := strings.Split(position, ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}
	colPart, _, _ := strings.Cut(parts[2], "#")
	line, lineErr := strconv.Atoi(parts[1])
	col, colErr := strconv.Atoi(colPart)
	if lineErr != nil || colErr != nil {
		return "", 0, 0, false
	}
	return parts[0], line, col, true
}

// validateBracketPaths refuses declarations the observation bracket
// cannot honor, loudly and before any measurement: an absolute external
// directory cannot be walked by the bracket's hashing semantics (its
// capture would seal every observation in the run - strictly worse than
// not declaring), and a path under a tool-excluded directory would be
// silently uncovered (REQ-exec-observation).
func validateBracketPaths(paths []string) error {
	for _, p := range paths {
		if filepath.IsAbs(p) {
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				return fmt.Errorf("gomutant: bracket path %s is an absolute directory the observation bracket cannot walk; declare it module-relative or declare the files it contains", p)
			}
			continue
		}
		clean := path.Clean(filepath.ToSlash(p))
		for _, excluded := range []string{".git", ".stipulator", ".gomutant"} {
			if clean == excluded || strings.HasPrefix(clean, excluded+"/") {
				return fmt.Errorf("gomutant: bracket path %s lies under tool-excluded %s and would be silently uncovered", p, excluded)
			}
		}
	}
	return nil
}

// preflightBracketPaths proves each declared bracket path exists and
// hashes against one measured module's root - the same base the
// per-spawn capture resolves against - refusing before that module's
// first spawn instead of burning a campaign whose every observation
// would seal at finalization: the field failure this guards against
// declared a transient per-test directory that test cleanup deleted
// before end-bracket hashing (REQ-exec-observation). A surface the
// oracle reads exists before the run; a path that legitimately churns
// wants a scratch namespace, not a bracket path.
func preflightBracketPaths(ctx context.Context, moduleDir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(moduleDir)
	if err != nil {
		return fmt.Errorf("gomutant: bracket path preflight: %w", err)
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(root, filepath.FromSlash(p))
		}
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("gomutant: bracket path %s does not exist at run start (%v); a transient per-test path wants --scratch-namespace, not a bracket path", p, err)
		}
		// Hashability preflight mirrors the bracket's tolerance classes
		// (regular files, directories, symlinks hash; anything else is
		// recorded unhashable and seals at ingest) - capture itself
		// tolerates unhashable entries, so the walk is the only place a
		// refusal can happen before the campaign burns.
		err := filepath.WalkDir(full, func(entry string, d iofs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if kind := d.Type(); !d.IsDir() && !kind.IsRegular() && kind&iofs.ModeSymlink == 0 {
				return fmt.Errorf("irregular entry %s (%s)", entry, kind)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("gomutant: bracket path %s preflight failed (%v); refusing before measurement rather than sealing every observation", p, err)
		}
	}
	return nil
}

func (t *Tree) Run(ctx context.Context, targets []Target, opts Options) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Budget < 0 {
		return nil, fmt.Errorf("gomutant: budget must be non-negative")
	}
	if opts.PlanOnly {
		// The plan clause's no-write guarantee is the run's own: even a
		// cached serve's incremental commit is suppressed here, so no
		// caller has to re-implement the suppression.
		opts.Commit = nil
	}
	if opts.OracleTimeout < 0 {
		return nil, fmt.Errorf("gomutant: oracle timeout must be non-negative")
	}
	if err := validateBracketPaths(opts.BracketPaths); err != nil {
		return nil, err
	}
	for _, namespace := range opts.ScratchNamespaces {
		if err := runtimeinput.ValidateScratchNamespace(namespace.Dir, namespace.Pattern); err != nil {
			return nil, fmt.Errorf("gomutant: scratch namespace %s:%s refused before measurement: %w", namespace.Dir, namespace.Pattern, err)
		}
	}
	// Bracket-path preflight is per measured module (the base the
	// per-spawn capture resolves against), memoized, at group
	// formation - before that module's first spawn.
	bracketPreflights := map[string]error{}
	if opts.OracleTimeout == 0 {
		opts.OracleTimeout = 60 * time.Second
	}
	opts = lockCallbacks(opts)
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
	residue := sync.OnceValue(func() string { return measurementResidue(ctx, repository, runStart) })
	// driftedMu guards drifted: the preparation goroutine's cached-serve
	// refusal and the aggregation loop's refusals append concurrently
	// (the pipeline runs preparation ahead of execution).
	var driftedMu sync.Mutex
	var drifted []TargetDrift
	refuseTarget := func(symbol, reason string) {
		driftedMu.Lock()
		drifted = append(drifted, TargetDrift{Symbol: symbol, Reason: reason})
		driftedMu.Unlock()
	}

	preparation := newRunPreparation(t)
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	engine.SetOracleMemoryLimit(opts.OracleMemoryBytes, jobs)
	// The inner-parallelism cap is a scheduling bound, deliberately
	// unpinned: it reaches verdicts through the wall-clock oracle
	// timeout like ambient load, and through the recorded environment
	// evidence exactly where an oracle observably reads it
	// (REQ-exec-oracle-parallelism). Installed before the subject
	// engines: their evidence env - the revalidation and producer env -
	// captures the delivered width at construction.
	engine.SetOracleParallelism(jobs)
	// The campaign's one evidence environment, mode-independent (env plus
	// the installed width; the per-mode engine sets are built after
	// resolution, when each target's attestation is known): every oracle
	// spawn, ingest mirror, merge, and splice below judges under this
	// single width-composed value, so a mid-campaign move of the
	// process-wide width atomic (a scoped probe override in a long-lived
	// server) cannot split the campaign's evidence - the engine-level
	// compositions are idempotent on an already-composed environment.
	runEnv := engine.OracleEvidenceEnv(t.eng.GoEnv())
	// The pin the run's evidence records and compares: resolved once, so
	// gates never read ambient process state.
	oracleMemoryPin := engine.OracleMemoryLimitBytes()
	// First match wins; duplicate symbols occur only in hand-edited
	// documents.
	prior := map[string]*Finding{}
	for i := range opts.Prior {
		f := &opts.Prior[i]
		if _, ok := prior[f.Symbol]; !ok {
			prior[f.Symbol] = f
		}
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
	}
	var resolvedTargets []resolvedTarget
	type baselineKey struct {
		pkg, run, flags, moduleDir, packageDir string
	}
	baselineCache := map[baselineKey]runtimeinput.Observation{}
	// scopedBaselines memoizes killer-scoped baseline probes for the
	// serial confirmation's differential ground — Run-local like
	// baselineCache, which is what makes the key complete (bounds,
	// brackets, namespaces, and the injected env are per-Run pins)
	// and the lock-free access true (this Run's probe gate serializes
	// every confirmation; a Tree-scoped map would race across the
	// MCP server's concurrent campaigns over one cached Tree).
	scopedBaselines := map[scopedBaselineKey]bool{}
	coverageCache := map[string]engine.Coverage{}
	guidanceCache := map[string]oracleAttribution{}
	decisions := make([]RunDecision, len(targets))
	for i, tg := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationResolving, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Labels: tg.Labels, OperatorSet: engine.OperatorSet, OracleExplicit: tg.OracleExplicit || len(tg.Oracle) != 0, OracleTimeout: opts.OracleTimeout.String(), OracleMemoryBytes: oracleMemoryPin}
		if tg.Shaped() {
			// Validation precedes oracle derivation: a shaped identity
			// resolves to no package, so deriving an oracle from it
			// would be a run fault where the contract wants a
			// target-local refusal (an explicit oracle is required).
			if err := validateShapedTarget(tg); err != nil {
				f.Skipped = "shaped target refused: " + err.Error()
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
				continue
			}
		}
		oracle, err := preparation.oracle(ctx, tg)
		if err != nil {
			return nil, err
		}
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default).
			f.Skipped = "no oracle"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
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
			f.Skipped = "oracle validation failed: " + err.Error()
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		if tg.Shaped() {
			// A shaped target resolves from its declared parameters:
			// no body, no catalog, its own pin (REQ-target-structural,
			// REQ-target-manual-recipes). Refusals are target-local
			// skips exactly as resolution failures are.
			candidates, digest, err := t.shapedCandidates(ctx, tg)
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
					reason := "shaped source drifted since load: " + err.Error() + " - re-run when the tree settles" + residue()
					refuseTarget(tg.Symbol, reason)
					decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: reason}
					continue
				}
				f.Skipped = "shaped resolution failed: " + err.Error()
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
				continue
			}
			f.BodyHash = digest
			f.OperatorSet = shapedOperatorSet
			f.Shape = &TargetShape{Structural: tg.Structural, Manual: tg.Manual}
			resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle, shaped: candidates, shapedDigest: digest, attested: packageProcessAttestable(tg.Symbol, oracle)})
			continue
		}
		bodyHash, err := t.eng.BodyHashContext(ctx, tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			f.Skipped = "not a function - for mutation adequacy, target its methods or the bound function-level subjects"
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
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
				reason := "source vanished since load: " + err.Error() + " - re-run when the tree settles" + residue()
				refuseTarget(tg.Symbol, reason)
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: reason}
				continue
			}
			f.Skipped = "target resolution failed: " + err.Error()
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			continue
		}
		f.BodyHash = bodyHash
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationFreshness, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolvedTargets = append(resolvedTargets, resolvedTarget{index: i, oracle: oracle, attested: packageProcessAttestable(tg.Symbol, oracle)})
	}
	// Per-mode view bundles: each target's attestation is its own (the
	// pairing above), so a mixed run builds one engine and view set per
	// mode present rather than dropping the attestation run-wide — the
	// coupling that made an unrelated cross-package target re-measure a
	// same-package sibling's verifiable records.
	type modeViews struct {
		engines        *subjectEngines
		symbols        []string
		views          *subjectViewSet
		viewFaults     map[string]error
		producerUnion  *subjectViewSet
		producerFaults map[string]error
		producerBuilt  bool
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
				engines:        t.newSubjectEngines(opts.AnalysisProgress, attested),
				views:          &subjectViewSet{bySymbol: map[string]*subjectView{}},
				viewFaults:     map[string]error{},
				producerUnion:  &subjectViewSet{bySymbol: map[string]*subjectView{}},
				producerFaults: map[string]error{},
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
		var err error
		mv.views, mv.viewFaults, err = t.newSubjectViewsFaultTolerant(ctx, mv.symbols, preparation.packageContext, mv.engines)
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
			f := &findings[rt.index]
			f.Skipped = "decision evidence unavailable: " + cause.Error()
			decisions[rt.index] = RunDecision{Symbol: targets[rt.index].Symbol, Action: "skipped", Reason: f.Skipped}
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
	// One observed union over every target and oracle replaces the
	// per-target proof builds the campaign previously paid (the measured
	// ~270 observation passes per warm campaign): per-subject evidence is
	// identical by gofresh's batch-equivalence contract, per-symbol
	// faults stay target-local, and the per-target build survives only
	// as the bounded retry (REQ-exec-quiescence). The union is built at
	// the first target that needs a proof: a fully-cached warm run —
	// every target served — pays no observation pass at all.
	// The union build runs observed captures (test processes) under the
	// probe gate held shared, exactly like the per-target retry builds:
	// with one union per MODE present, a later mode's first measure target
	// prepares while earlier targets' windows execute (preparation is
	// pipelined with execution), so a serial confirmation can be in
	// flight — the gate, not any ordering premise, is what keeps the
	// build's processes out of a confirmation's isolation window.
	buildProducerUnion := func(mv *modeViews) error {
		if mv.producerBuilt {
			return nil
		}
		mv.producerBuilt = true
		if opts.proofAttempt != nil {
			opts.proofAttempt("", 1)
		}
		probeGate.RLock()
		defer probeGate.RUnlock()
		var err error
		mv.producerUnion, mv.producerFaults, err = t.newObservedUnionViews(ctx, mv.symbols, preparation.packageContext, mv.engines)
		if err != nil {
			return fmt.Errorf("freshness proofs (union over %d subjects): %w", len(mv.symbols), err)
		}
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
	// recorded reason without re-probing.
	baselineFailures := map[baselineKey]string{}
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
		for _, group := range w.groups {
			key := baselineKey{pkg: group.pkgs[0], run: group.runRegex, flags: strings.Join(group.flags, "\x00"), moduleDir: group.moduleDir, packageDir: group.packageDir}
			if reason, failed := baselineFailures[key]; failed {
				return reason, nil
			}
			state, ok := baselineCache[key]
			if !ok {
				reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0]})
				if err := ctx.Err(); err != nil {
					return "", err
				}
				probeGate.RLock()
				ran, passed, failedTests, observed, err := engine.TestProbeObservedEnv(ctx, t.dir, group.pkgs[0], group.runRegex, opts.OracleTimeout, group.flags, group.moduleDir, group.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
				probeGate.RUnlock()
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
				if reason != "" {
					baselineFailures[key] = reason
					return reason, nil
				}
				state = observed
				if err := ctx.Err(); err != nil {
					return "", err
				}
				baselineCache[key] = state
			}
			w.baselines = append(w.baselines, state)
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
		// package pins its draws (REQ-exec-property-oracles).
		targetRapid, err := preparation.rapidPackages(ctx, oraclePackages)
		if err != nil {
			return nil, err
		}
		regime := ""
		for _, run := range pkgRuns(oracle) {
			if targetRapid[run.pkg] {
				regime = engine.PropertyRegimeRapid
				break
			}
		}
		f.PropertyRegime = regime
		reason := "no-prior"
		var decision RunDecision
		if hasPrior && opts.Force {
			reason = "forced"
		}
		if hasPrior && !opts.Force {
			matches, err := shapedEvidenceMatchesContext(ctx, *rec, oracleViews, shapedOperatorSet, opts.OracleTimeout.String(), oracleMemoryPin, regime)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				reason := serveCheckRefusal(err)
				refuseTarget(tg.Symbol, reason+residue())
				f.Skipped = reason
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
				if opts.Decision != nil {
					opts.Decision(decisions[i])
				}
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
				served.Labels = append([]string(nil), tg.Labels...)
				served.Cached = true
				if stagedDrift, err := t.stampProvenance(ctx, repository, nil, oracleViews, &served); err != nil {
					return nil, err
				} else if stagedDrift != "" {
					refuseTarget(tg.Symbol, stagedDrift+residue())
					f.Skipped = "staged drift: " + stagedDrift
					decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
					if opts.Decision != nil {
						opts.Decision(decisions[i])
					}
					return nil, nil
				}
				*f = served
				decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: "served: shape and oracle pins unchanged"}
				if opts.Decision != nil {
					opts.Decision(decisions[i])
				}
				return nil, nil
			}
			reason = "stale"
			if !matches {
				reason = "stale: an oracle or measurement pin moved"
			} else if rec.BodyHash != resolved.shapedDigest {
				reason = "stale: the declared shape or a probed file moved"
			}
		}
		// The measure path attaches evidence, so it needs the OBSERVED
		// producer views — the decision views above serve only the pin
		// checks; a shaped identity is not in the union, so the subset
		// derives from the oracle symbols alone.
		if err := buildProducerUnion(mv); err != nil {
			return nil, err
		}
		producerViews, err := mv.producerUnion.forTarget(oracle[0], oracle[1:], mv.producerFaults)
		if err != nil && ctx.Err() == nil {
			probeGate.RLock()
			producerViews, err = t.newSubjectViewsWithPackageContext(ctx, oracle, preparation.packageContext, true, mv.engines)
			probeGate.RUnlock()
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			f.Skipped = "producer evidence unavailable: " + err.Error()
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped}
			if opts.Decision != nil {
				opts.Decision(decisions[i])
			}
			return nil, nil
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
		oracleViews = oracleViews[:0]
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
		}
		for _, module := range producerViews.modules {
			module.producer = true
		}
		item := work{target: i, oracle: oracle, reason: reason, shaped: true, candidates: resolved.shaped, oracleViews: oracleViews, producer: producerViews}
		w := &item
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		w.oracleSet = oracleSet
		decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason, Candidates: len(w.candidates)}
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
			return nil, err
		}
		for _, pr := range runs {
			flags := propertyOracleFlags(slices.Contains(runtimes[pr.pkg], "rapid"))
			moduleDir, packageDir, err := preparation.packageContext(ctx, pr.pkg)
			if err != nil {
				return nil, err
			}
			if _, seen := bracketPreflights[moduleDir]; !seen {
				bracketPreflights[moduleDir] = preflightBracketPaths(ctx, moduleDir, opts.BracketPaths)
			}
			if err := bracketPreflights[moduleDir]; err != nil {
				return nil, err
			}
			w.groups = append(w.groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		if reason, err := probeGroupBaselines(ctx, tg, w); err != nil {
			return nil, err
		} else if reason != "" {
			f.Skipped = reason
			if opts.Decision != nil {
				opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: reason})
			}
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
			reason := "source changed since load: " + sourceDrift.Path + " - re-run when the tree settles" + residue()
			refuseTarget(tg.Symbol, reason)
			decisions[i] = RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: reason}
			if opts.Decision != nil {
				opts.Decision(decisions[i])
			}
			return true
		}
		rec, hasPrior := prior[tg.Symbol]
		// The target's property-runtime measurement regime: a rapid
		// oracle package pins its draws, and the regime is a measurement
		// pin - a record measured under other draws re-measures
		// (REQ-exec-property-oracles).
		targetRapid, err := preparation.rapidPackages(ctx, oraclePackages)
		if err != nil {
			return nil, err
		}
		regime := ""
		for _, run := range pkgRuns(oracle) {
			if targetRapid[run.pkg] {
				regime = engine.PropertyRegimeRapid
				break
			}
		}
		f.PropertyRegime = regime
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
		var grow *Finding
		var growAdded []string
		var drift *Finding
		var driftMoved []string
		var driftAdded []string
		// refuseServeCheck is the serve block's target-local exit for a
		// failed evidence check (serveCheckRefusal, any non-ctx error):
		// the condition is this target's own, the campaign proceeds.
		refuseServeCheck := func(reason string) {
			refuseTarget(tg.Symbol, reason+residue())
			if opts.Decision != nil {
				opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "refused", Reason: reason})
			}
		}
		if hasPrior && !opts.Force && budgetCovers(*rec, opts.Budget) {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), oracleMemoryPin, regime)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				refuseServeCheck(serveCheckRefusal(err))
				return nil, nil
			}
			if !matches {
				// A mismatch may be exactly the growth the third carve-out
				// serves: the derived oracle grew while the compartment
				// moved by an inert declaration delta (REQ-result-stale).
				added, grows, gerr := evidenceSetCoversGrowthContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), oracleMemoryPin, regime)
				if gerr != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					refuseServeCheck(serveCheckRefusal(gerr))
					return nil, nil
				}
				if grows {
					snapshot := snapshotFindings([]Finding{*rec})[0]
					grow = &snapshot
					growAdded = added
				} else if moved, addedOracles, drifts, derr := evidenceSetCoversKillerDriftContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), oracleMemoryPin, regime); derr != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					refuseServeCheck(serveCheckRefusal(derr))
					return nil, nil
				} else if drifts {
					// The compartment moved but the movement is attributable
					// (a grown set composing as added oracles): kills keyed
					// to unmoved oracles stand, the rest re-measures
					// (REQ-result-stale's killer-drift carve-out).
					snapshot := snapshotFindings([]Finding{*rec})[0]
					drift = &snapshot
					driftMoved = moved
					driftAdded = addedOracles
				} else {
					// The moved pin is named so a caller who just wrote
					// kill-tests sees the tool noticing them instead of
					// forcing defensively (REQ-result-stale). The class comes
					// from the inspection, not an assumed "stale": an
					// unverifiable prior is not stale. The attribution may
					// build a supplementary view for a recorded oracle the
					// current resolution lacks — analysis-pass work gated
					// shared exactly like the union build above.
					probeGate.RLock()
					reason = t.movedPinAttribution(ctx, *rec, mv.views, "stale: a measurement pin moved (oracle timeout, oracle selection, operator set, or runtime inputs moved during evaluation)")
					probeGate.RUnlock()
				}
			}
			if matches && len(rec.CandidateEvidence) == 0 {
				cached := *rec
				cached.Labels = append([]string(nil), tg.Labels...)
				cached.Cached = true
				// Matched pins make the current view's ledger identical to
				// the record's; stamping it is the conformance upgrade for a
				// record that predates the ledger (REQ-result-record).
				if ledger, lerr := targetView.view.TestVariantLedger(targetView.subject); lerr == nil {
					cached.CompartmentLedger = compartmentLedgerFromView(ledger)
				} else {
					// A subject missing from its own view is an internal
					// fault, not tree drift — the ledger read consults the
					// capture, never disk.
					return nil, lerr
				}
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
				if stagedDrift, err := t.stampProvenance(ctx, repository, targetView, oracleViews, &cached); err != nil {
					return nil, err
				} else if stagedDrift != "" {
					refuseTarget(tg.Symbol, stagedDrift+residue())
					if opts.Decision != nil {
						opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "refused", Reason: stagedDrift})
					}
					return nil, nil
				}
				findings[i] = cached
				// Commit precedes the decision: a caller canceling at the
				// decision callback still holds the persisted finding
				// (REQ-exec-cancellation's incremental-commit clause).
				if err := commitFinding(ctx, opts.Commit, cached); err != nil {
					return nil, err
				}
				if opts.Decision != nil {
					opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: "served: body, oracle closure, and runtime inputs unchanged"})
				}
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
			} else {
				matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), oracleMemoryPin, regime)
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

		if err := buildProducerUnion(mv); err != nil {
			return nil, err
		}
		producerViews, err := mv.producerUnion.forTarget(tg.Symbol, oracle, mv.producerFaults)
		if err != nil && ctx.Err() == nil {
			// One bounded retry (REQ-exec-quiescence): the field failure
			// mode is transient pressure faulting one symbol or module
			// group out of the shared union, gone by the time anyone
			// reads the fault. The retry rebuilds this target's own proof
			// surface — the demoted per-target build — so a transient
			// union fault costs one extra pass for one target, never a
			// skip.
			if opts.proofAttempt != nil {
				opts.proofAttempt(tg.Symbol, 2)
			}
			probeGate.RLock()
			producerViews, err = t.newSubjectViewsWithPackageContext(ctx, append([]string{tg.Symbol}, oracle...), preparation.packageContext, true, mv.engines)
			probeGate.RUnlock()
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The campaign itself is canceled: abort is the answer.
				return nil, proofAbortError(tg.Symbol, oracle, err, ctxErr)
			}
			// A per-target evidence condition, target-local by the same
			// rule as drift refusal (REQ-exec-quiescence): this target
			// skips with the cause on its decision line — a skip never
			// overwrites a prior record — and the campaign proceeds.
			f.Skipped = fmt.Sprintf("freshness proof unavailable (oracle %s): %v", strings.Join(oracle, ", "), err)
			if opts.Decision != nil {
				opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: f.Skipped})
			}
			return nil, nil
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
		targetView = producerViews.bySymbol[tg.Symbol]
		oracleViews = oracleViews[:0]
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
		}
		oracleSet := make(map[string]bool, len(oracle))
		for _, o := range oracle {
			oracleSet[o] = true
		}
		for _, module := range producerViews.modules {
			module.producer = true
		}
		currentLedger, err := targetView.view.TestVariantLedger(targetView.subject)
		if err != nil {
			return nil, err
		}
		item := work{target: i, oracle: oracle, reason: reason, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, producer: producerViews, currentLedger: currentLedger, serve: serve, extend: extend, grow: grow, growAdded: growAdded, drift: drift, driftMoved: driftMoved, driftAdded: driftAdded}
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
		if w.grow != nil {
			// Same discipline for growth: the record's own selection is what
			// its survivors re-identify against — a covering but different
			// request budget must not manufacture a refusal that would
			// destroy a serviceable record (REQ-result-stale's growth
			// carve-out); the grown record keeps its recorded budget, which
			// covers the request by the arm's own gate.
			budget = w.grow.Budget
		}
		if w.drift != nil {
			// Same discipline again for killer drift: the record's own
			// selection is what its kills and survivors re-identify against
			// (REQ-result-stale's killer-drift carve-out).
			budget = w.drift.Budget
		}
		// regenerateAtBudget re-enumerates at the request budget when a
		// carve-out's record-shaped regeneration cannot be spliced —
		// one exit for the three carve-out fallbacks (drift skips
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
				decision = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: fmt.Sprintf("served: pins unchanged; re-executing %s", candidateNoun(len(flagged))), Candidates: len(flagged)}
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
				decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: fmt.Sprintf("served: prefix of %s stands; measuring %d more", candidateNoun(w.extendFrom), suffix), Candidates: suffix}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// prefix, so the record cannot be extended: the whole target
				// re-measures (REQ-result-stale).
				w.extend = nil
				w.reason += "; deterministic regeneration cannot re-identify the measured prefix"
			}
		}
		if w.grow != nil {
			if survivors, ok := grownSurvivorIndexes(generation, *w.grow); ok {
				w.candidates = generation.Candidates
				w.growSurvivors = survivors
				decision = RunDecision{Symbol: tg.Symbol, Action: "measure",
					Reason:     fmt.Sprintf("served: derived oracle grew by %s; re-measuring %s against them", testNoun(len(w.growAdded)), survivorNoun(len(survivors))),
					Candidates: len(survivors)}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// candidates and survivors, so the record cannot grow: the
				// whole target re-measures under the request's own budget,
				// with the reason naming what actually happened — the record
				// is not stale, its oracle grew (REQ-result-stale).
				w.grow = nil
				w.reason = fmt.Sprintf("derived oracle grew by %s, but deterministic regeneration cannot re-identify the recorded candidates and survivors", testNoun(len(w.growAdded)))
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
		if w.drift != nil {
			if remeasure, stand, flagged, ok := driftRemeasureIndexes(generation, *w.drift, w.driftMoved, w.driftAdded); ok {
				w.candidates = generation.Candidates
				w.driftRemeasure = remeasure
				reason := fmt.Sprintf("served: %s stand on unmoved oracles; re-measuring %s against the current oracle", killNoun(stand), candidateNoun(len(remeasure)))
				if len(w.driftAdded) != 0 {
					reason += fmt.Sprintf(" (derived oracle grew by %s)", testNoun(len(w.driftAdded)))
				}
				if flagged != 0 {
					reason += fmt.Sprintf("; %s re-execute%s flagged evidence", candidateNoun(flagged), map[bool]string{true: "s"}[flagged == 1])
				}
				if len(remeasure) == 0 && len(w.driftAdded) == 0 {
					// Flagged evidence always re-measures, so an empty set
					// here means nothing moved, nothing flagged, and — by
					// the guard — nothing added: the no-reach serve. With
					// added tests and an empty set (a fully-killed record),
					// the grown wording above stands.
					reason = "served: compartment delta reaches no recorded oracle; nothing re-measures"
				}
				decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason, Candidates: len(remeasure)}
			} else {
				// Deterministic regeneration cannot re-identify the recorded
				// candidates, kills, and survivors, so the record cannot
				// serve under drift: the whole target re-measures under the
				// request's own budget (REQ-result-stale).
				w.drift = nil
				w.reason = "killer drift is attributable, but deterministic regeneration cannot re-identify the recorded candidates, kills, and survivors"
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
		if w.serve == nil && w.extend == nil && w.grow == nil && w.drift == nil {
			w.candidates = generation.Candidates
			decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: w.reason, Candidates: len(generation.Candidates)}
			f.Budget = opts.Budget
			f.CandidateCount = generation.CandidateCount
			f.Generated = len(generation.Candidates)
		}
		// Per-package oracle scoping (REQ-exec-oracle-run), with the rapid
		// failfile flag only in front of binaries that register it
		// (REQ-mut-overlay). A growth serve builds its groups over the added
		// tests alone — the recorded kills already rest on the recorded set
		// (REQ-core-attributed-kills) — each delta group earning its own
		// baseline below, so a failing added test skips this target with
		// the failure named (REQ-exec-quiescence's baseline locality).
		if opts.PlanOnly {
			// The plan needs candidate counts and decisions, never
			// baseline probes: group construction and probing are
			// execution cost the plan exists to preview.
			if opts.Decision != nil {
				opts.Decision(decision)
			}
			return nil, nil
		}
		groupOracle := w.oracle
		if w.grow != nil {
			groupOracle = w.growAdded
		}
		runs := pkgRuns(groupOracle)
		runtimes, err := preparation.propertyRuntimes(ctx, oraclePackages)
		if err != nil {
			return nil, err
		}
		for _, pr := range runs {
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
			if _, seen := bracketPreflights[moduleDir]; !seen {
				bracketPreflights[moduleDir] = preflightBracketPaths(ctx, moduleDir, opts.BracketPaths)
			}
			if err := bracketPreflights[moduleDir]; err != nil {
				return nil, err
			}
			w.groups = append(w.groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		if reason, err := probeGroupBaselines(ctx, tg, w); err != nil {
			return nil, err
		} else if reason != "" {
			f.Skipped = reason
			if opts.Decision != nil {
				opts.Decision(RunDecision{Symbol: tg.Symbol, Action: "skipped", Reason: reason})
			}
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
			if runTruncateAfterItems > 0 && delivered >= runTruncateAfterItems {
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
			// With runTruncateErr set it models the OTHER failure - a
			// preparation error that surfaces - so the error path's
			// held-window aggregation is testable.
			err = runTruncateErr
		}
		prepErr = err
	}()
	interner := &manifestInterner{byDigest: map[string]string{}}
	type job struct{ wi, mi int }
	// Execution proceeds in bounded target windows: each window's pool
	// drains, its kills confirm serially against an idle pool, and its
	// findings aggregate and COMMIT before the next window dispatches
	// (REQ-exec-cancellation's incremental-commit clause). One
	// campaign-global pool would hold every target's observations
	// resident until the end and commit nothing for hours, so a single
	// late abort cost the whole campaign's verdicts. Serial
	// confirmation's isolation contract is "alone after the pool
	// drains", which the per-window drain preserves.
	windowBudget := jobs * 8
	if windowBudget < 64 {
		windowBudget = 64
	}
	if runWindowCandidates > 0 {
		windowBudget = runWindowCandidates
	}
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
		findings[w.target] = f
		if err := commitFinding(ctx, opts.Commit, f); err != nil {
			return err
		}
		committed[w.target] = true
		return t.emitOracleGuidance(ctx, f, w, targets[w.target].Symbol, opts, runEnv, guidanceCache)
	}
	// Advisory execution progress rides window boundaries: totals are
	// exact, per-window timing is not part of the deterministic sequence
	// (REQ-exec-run-status's advisory classes).
	dispatchedTargets, mutantsDone := 0, 0
	type executedWindow struct {
		window       []work
		outcomes     [][]engine.MutantOutcome
		observations [][]runtimeinput.Observation
		incompletes  [][]string
		killers      [][]string
	}
	executeWindow := func(window []work) (*executedWindow, error) {
		outcomes := make([][]engine.MutantOutcome, len(window))
		observations := make([][]runtimeinput.Observation, len(window))
		incompletes := make([][]string, len(window))
		killers := make([][]string, len(window))
		for wi := range window {
			outcomes[wi] = make([]engine.MutantOutcome, len(window[wi].candidates))
			observations[wi] = make([]runtimeinput.Observation, len(window[wi].candidates))
			incompletes[wi] = make([]string, len(window[wi].candidates))
			killers[wi] = make([]string, len(window[wi].candidates))
		}
		reportExecuting(opts.Executing, ExecutionEvent{
			Phase:       "executing",
			TargetIndex: dispatchedTargets + 1, TargetCount: int(preparedTargets.Load()),
			Symbol:         targets[window[0].target].Symbol,
			CandidatesDone: mutantsDone, CandidatesTotal: int(preparedCandidates.Load()),
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
					w := window[j.wi]
					m, runnable := w.candidates[j.mi].Mutant()
					if !runnable {
						continue
					}
					outcome, killer, state, incompleteReason, err := t.executeWorkMutant(poolCtx, w, m, opts, runEnv)
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
					observations[j.wi][j.mi] = interner.intern(state)
					incompletes[j.wi][j.mi] = incompleteReason
					killers[j.wi][j.mi] = killer
					outcomes[j.wi][j.mi] = outcome
				}
			}()
		}
	dispatching:
		for wi := range window {
			for mi, candidate := range window[wi].candidates {
				if _, runnable := candidate.Mutant(); !runnable {
					continue
				}
				if window[wi].serve != nil && !window[wi].flagged[mi] {
					// A served record's covered candidates keep their recorded
					// outcomes; only the flagged ones re-execute
					// (REQ-result-stale).
					continue
				}
				if window[wi].extend != nil && mi < window[wi].extendFrom {
					// An extended record's measured prefix keeps its recorded
					// outcomes; only the unmeasured suffix executes
					// (REQ-mut-budget, REQ-result-stale's budget-extension
					// carve-out).
					continue
				}
				if window[wi].grow != nil && !window[wi].growSurvivors[mi] {
					// A grown record's kills and discards stand — a grown oracle
					// can only kill more — so only the recorded survivors
					// re-execute, against the added tests alone
					// (REQ-result-stale's growth carve-out).
					continue
				}
				if window[wi].drift != nil && !window[wi].driftRemeasure[mi] {
					// A drifted record's kills keyed to unmoved oracles and its
					// unflagged discards stand; only moved-killer kills,
					// set-wide kills under any movement, survivors under any
					// movement or growth, and flagged candidates re-execute,
					// against the full current oracle (REQ-result-stale's
					// killer-drift carve-out).
					continue
				}
				if opts.dispatched != nil {
					opts.dispatched(window[wi].candidates[mi].Symbol, mi)
				}
				select {
				case jobCh <- job{wi, mi}:
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
		// Serial kill confirmation (REQ-exec-attribution): a test-attributed
		// or package-scope kill measured beside sibling mutants re-executes
		// alone, and the serial execution is the scored one - outcome,
		// observation, and candidate evidence replaced wholesale - so
		// interference from a sibling never reads as a kill. Timeout kills
		// are excluded: confirming one costs the full timeout again, and the
		// hang bound is the caller's own budget - the named residual.
		for wi := range window {
			mutantsDone += len(window[wi].candidates)
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
					m, _ := window[wi].candidates[k.mi].Mutant()
					reportExecuting(opts.Executing, ExecutionEvent{
						Phase: "confirming",
						// The confirmed kill's own target, not the
						// window head: a window spans many targets
						// and the walk crosses them, so a head-pinned
						// index reads as a stuck campaign while the
						// symbol changes underneath it.
						TargetIndex: dispatchedTargets + wi + 1, TargetCount: int(preparedTargets.Load()),
						Symbol:         targets[window[wi].target].Symbol,
						CandidatesDone: mutantsDone, CandidatesTotal: int(preparedCandidates.Load()),
						ConfirmationsDone: confirmDone, ConfirmationsTotal: confirmTotal,
						ConfirmationMode: confirmMode,
					})
					probeGate.Lock()
					outcome, killer, state, incomplete, err := t.confirmMutant(ctx, window[wi], m, killers[wi][k.mi], scopedBaselines, opts, runEnv)
					probeGate.Unlock()
					if err != nil {
						return confirmInconclusive, err
					}
					confirmDone++
					initialKiller := killers[wi][k.mi]
					outcomes[wi][k.mi] = outcome
					killers[wi][k.mi] = killer
					observations[wi][k.mi] = interner.intern(state)
					incompletes[wi][k.mi] = incomplete
					classified := classifyConfirmation(outcome, killer)
					if classified == confirmFlipped {
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
		return &executedWindow{window: window, outcomes: outcomes, observations: observations, incompletes: incompletes, killers: killers}, nil
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
		window, outcomes, observations, incompletes, killers := st.window, st.outcomes, st.observations, st.incompletes, st.killers
		for wi := range window {
			w := window[wi]
			if err := ctx.Err(); err != nil {
				return err
			}
			if opts.aggregate != nil {
				opts.aggregate()
			}
			f := &findings[w.target]
			if w.serve != nil {
				spliced, err := t.spliceServedFinding(ctx, runEnv, *w.serve, w.candidates, w.flagged, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Exemptions)
				if err != nil {
					return err
				}
				stampExemptions(&spliced, opts.Exemptions)
				// The aggregated work item's retained observations are dead past
				// this point; releasing them per item keeps the run's peak at the
				// in-flight items rather than the whole campaign.
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(targets[w.target].Symbol, err.Error()+residue())
					continue
				}
				// Served prefix + re-executed candidates both validated
				// against the current tree; provenance recomputes like a
				// fresh measure's (REQ-result-stale, REQ-result-layers).
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, &spliced); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(targets[w.target].Symbol, stagedDrift+residue())
					continue
				}
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
			if w.grow != nil {
				grown, _, shed, err := t.spliceGrownFinding(ctx, runEnv, *w.grow, w, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Exemptions)
				if err != nil {
					return err
				}
				stampExemptions(&grown, opts.Exemptions)
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(targets[w.target].Symbol, err.Error()+residue())
					continue
				}
				// The grown record carries the current tree's evidence, so its
				// commit and dirty provenance are recomputed like a fresh
				// measure's rather than carried from the served record.
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, &grown); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(targets[w.target].Symbol, stagedDrift+residue())
					continue
				}
				// Advisory buckets re-derived honestly under the delta oracle:
				// an added test executing a previously never-executed survivor
				// upgrades its bucket; downgrades never happen — the recorded
				// bucket was measured under the full oracle — and a
				// divergence-stamped record's survivors were already classified
				// unstable by the counts fold.
				if !unstableForBuckets(&grown, opts.Exemptions) {
					coverage, probed, err := t.oracleCoverage(ctx, w, opts, runEnv, coverageCache)
					if err != nil {
						return err
					}
					if probed {
						coverPkg := w.targetView.subject.Package
						for si := range grown.Survivors {
							if !coverageUpgradeAllowed(grown.Survivors[si].Execution) {
								continue
							}
							if covered, ok := survivorCovered(coverage, coverPkg, grown.Survivors[si]); ok && covered {
								grown.Survivors[si].Execution = "executed-and-passed"
							}
						}
					}
				}
				// Evidence beats attestation: each shed disposition names its
				// killer so the contradiction — a mutant judged equivalent was
				// just distinguished — reaches a human (REQ-attest-survivor,
				// REQ-result-stale's growth carve-out). Fired BEFORE the
				// commit: the commit is where a consumer streams merge sheds,
				// and first-report-wins needs the contradiction on record by
				// then — the specific reason outranks the merge layer's
				// vaguer one. The accepted trade: a commit that then fails
				// (a canceled context) was preceded by a report about a
				// persist that never happened — the run aborts regardless,
				// while the inverse order re-opens the silent-strip hole
				// this ordering closes.
				if opts.Contradiction != nil && len(shed) != 0 {
					byIdentity, _ := candidateIdentityIndex(w.candidates)
					for _, attestation := range shed {
						killer := ""
						if mi, ok := byIdentity[survivorKey{attestation.Position, attestation.Operator}]; ok {
							killer = killers[wi][mi]
						}
						opts.Contradiction(AttestationContradiction{
							Symbol: targets[w.target].Symbol, Position: attestation.Position,
							Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
						})
					}
				}
				if err := commitAndAttribute(ctx, grown, w); err != nil {
					return err
				}
				continue
			}
			if w.drift != nil {
				spliced, _, shed, err := t.spliceDriftFinding(ctx, runEnv, *w.drift, w, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Exemptions)
				if err != nil {
					return err
				}
				stampExemptions(&spliced, opts.Exemptions)
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(targets[w.target].Symbol, err.Error()+residue())
					continue
				}
				// The drifted record carries the current tree's evidence — the
				// gate proved the retained movement is the attributable
				// compartment delta — so provenance is recomputed like a fresh
				// measure's (REQ-result-stale's killer-drift carve-out).
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, &spliced); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(targets[w.target].Symbol, stagedDrift+residue())
					continue
				}
				// With any oracle moved or the set grown, every surviving
				// candidate was re-measured against the full current
				// oracle, so advisory buckets re-derive from the current
				// probe exactly as a fresh measure's do; a no-reach serve
				// with nothing added re-measured nothing and carries every
				// recorded bucket verbatim, paying no coverage probe.
				if (len(w.driftMoved) != 0 || len(w.driftAdded) != 0) && !unstableForBuckets(&spliced, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, coverageCache, 0); err != nil {
						return err
					}
				}
				// Evidence beats attestation, exactly as under growth: a
				// re-measured attested survivor a moved test now kills sheds
				// its attestation with the contradiction reported — before
				// the commit, so a commit-time shed stream finds it on
				// record (first-report-wins).
				if opts.Contradiction != nil && len(shed) != 0 {
					byIdentity, _ := candidateIdentityIndex(w.candidates)
					for _, attestation := range shed {
						killer := ""
						if mi, ok := byIdentity[survivorKey{attestation.Position, attestation.Operator}]; ok {
							killer = killers[wi][mi]
						}
						opts.Contradiction(AttestationContradiction{
							Symbol: targets[w.target].Symbol, Position: attestation.Position,
							Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
						})
					}
				}
				if err := commitAndAttribute(ctx, spliced, w); err != nil {
					return err
				}
				continue
			}
			if w.extend != nil {
				extended, err := t.spliceExtendedFinding(ctx, runEnv, *w.extend, w.candidates, w.extendFrom, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes[wi], killers[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Budget, opts.Exemptions)
				if err != nil {
					return err
				}
				stampExemptions(&extended, opts.Exemptions)
				// Same release as the served branch: the splice is computed, the
				// per-candidate observations are dead.
				observations[wi] = nil
				if err := w.producer.validateProducers(ctx); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					refuseTarget(targets[w.target].Symbol, err.Error()+residue())
					continue
				}
				// Advisory execution buckets: a verifiable extension's suffix
				// survivors earn theirs from the current probe like any measured
				// run's, while carried prefix survivors keep their recorded
				// buckets verbatim; a divergence-stamped extension's suffix
				// survivors were already classified unstable by the splice.
				if !unstableForBuckets(&extended, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, coverageCache, len(w.extend.Survivors)); err != nil {
						return err
					}
				}
				// Served prefix + measured suffix both validated against the
				// current tree; provenance recomputes like a fresh measure's
				// (REQ-result-stale, REQ-result-layers).
				if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, &extended); err != nil {
					return err
				} else if stagedDrift != "" {
					refuseTarget(targets[w.target].Symbol, stagedDrift+residue())
					continue
				}
				if err := commitAndAttribute(ctx, extended, w); err != nil {
					return err
				}
				continue
			}
			unionCandidates := w.candidates
			unionOutcomes := outcomes[wi]
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
			if w.shaped {
				// A shaped finding carries oracle evidence only: its
				// subject is the declared shape, pinned by the shape
				// digest in BodyHash, never a resolvable symbol
				// (REQ-target-structural, REQ-target-manual-recipes).
				oracleEvidence, err := attachOracleEvidence(w.oracleViews, state)
				if err != nil {
					return err
				}
				f.OracleEvidence = oracleEvidence
			} else {
				targetEvidence, oracleEvidence, err := attachEvidence(w.targetView, w.oracleViews, state)
				if err != nil {
					return err
				}
				f.TargetEvidence = targetEvidence
				f.OracleEvidence = oracleEvidence
				f.CompartmentLedger = compartmentLedgerFromView(w.currentLedger)
			}
			if err := w.producer.validateProducers(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				refuseTarget(targets[w.target].Symbol, err.Error()+residue())
				continue
			}
			if stagedDrift, err := t.stampProvenance(ctx, repository, w.targetView, w.oracleViews, f); err != nil {
				return err
			} else if stagedDrift != "" {
				refuseTarget(targets[w.target].Symbol, stagedDrift+residue())
				continue
			}
			stampExemptions(f, opts.Exemptions)
			f.Operators = summarizeOperators(w.candidates, outcomes[wi])
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
				switch outcomes[wi][mi] {
				case engine.MutantSurvived:
					f.Survivors = append(f.Survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent})
				case engine.MutantKilled:
					// The keystone persisted: every kill names its killer
					// (REQ-core-attributed-kills), so reuse can key the kill
					// to its killer's content (REQ-result-stale's
					// killer-drift carve-out).
					f.Kills = append(f.Kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[wi][mi]})
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
					return sameAttestationPins(rec, *f)
				}
				return mutationDomainHeld(rec, *f)
			}
			if rec, ok := prior[targets[w.target].Symbol]; ok && domainHeld(*rec) {
				kept, siteSheds, gone := carryAnchoredAttestations(rec.Attested, f.Survivors)
				f.Attested = append(f.Attested, kept...)
				// A carry across moved pins is an acceptance that outlives
				// the environment it was judged in - reported distinctly
				// at the moment it rides, so it is auditable
				// (REQ-attest-survivor). A pins-held carry restores the
				// record verbatim and stays quiet, as it always has.
				if !sameAttestationPins(*rec, *f) && opts.AttestationCarried != nil {
					for _, attestation := range kept {
						opts.AttestationCarried(AttestationCarry{
							Symbol: f.Symbol, Position: attestation.Position,
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
	for {
		window, ok := gatherWindow(items, windowBudget)
		if !ok {
			break
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
		// The previously executed window aggregates only now, after the
		// next window's items arrived and proved it was not the last:
		// its commits still land before this window dispatches
		// (REQ-exec-cancellation's incremental-commit clause), and in the
		// common case — preparation running ahead of execution through the
		// buffered channel — the gather returns instantly, so the hold
		// costs nothing.
		if err := aggregateHeld(); err != nil {
			return nil, err
		}
		executed, err := executeWindow(window)
		if err != nil {
			return nil, err
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
	if len(drifted) > 0 || treeDrift != nil {
		completed := completedFindings(findings, drifted)
		if len(drifted) == 0 {
			return completed, &TreeDriftError{Completed: len(completed), Transient: treeDrift.Error()}
		}
		return completed, &TreeDriftError{Drifted: drifted, Completed: len(completed)}
	}
	return findings, nil
}

func snapshotTargets(targets []Target) []Target {
	snapshot := slices.Clone(targets)
	for i := range snapshot {
		snapshot[i].Oracle = slices.Clone(snapshot[i].Oracle)
		snapshot[i].Labels = slices.Clone(snapshot[i].Labels)
	}
	return snapshot
}

func snapshotFindings(findings []Finding) []Finding {
	snapshot := slices.Clone(findings)
	for i := range snapshot {
		snapshot[i] = cloneFinding(snapshot[i])
	}
	return snapshot
}

// runWindowCandidates overrides the execution window candidate budget
// when positive - a test seam; zero means the jobs-derived default.
var runWindowCandidates int

// runTruncateAfterItems, when positive, makes the preparation pipeline
// stop delivering work after that many items with the stopping error
// lost - a test seam reproducing the truncation shape
// REQ-exec-completion refuses. Production never sets it.
var runTruncateAfterItems int

var errTruncateSeam = errors.New("truncation seam")

// runTruncateErr, when non-nil beside runTruncateAfterItems, is
// surfaced as the preparation pipeline's own error instead of being
// swallowed: the seam for the error path's held-window aggregation -
// completed windows, the held one included, commit before the error
// returns (REQ-exec-attribution's abort terms).
var runTruncateErr error

// gatherWindow blocks for the next prepared work item and keeps blocking
// until the window's candidate budget is met or preparation ends. Blocking —
// never draining just what is buffered — is what keeps window boundaries a
// pure function of target order and the budget rule, independent of
// execution timing, so the window-scoped confirmation flip signal covers
// the same kills on every run (REQ-exec-attribution). ok is false when no
// item remains.
func gatherWindow(items <-chan work, budget int) ([]work, bool) {
	first, ok := <-items
	if !ok {
		return nil, false
	}
	window := []work{first}
	for total := len(first.candidates); total < budget; {
		next, ok := <-items
		if !ok {
			break
		}
		window = append(window, next)
		total += len(next.candidates)
	}
	return window, true
}

// serveCheckRefusal renders a serve-time evidence-check failure as that
// target's own refusal reason: the check re-observes the view against
// disk when it closes over the record's runtime-input evidence, so a
// content move surfaces as a view-changed error and a move that breaks
// the typed load as a plain one — either way the condition is the
// target's, refused target-locally exactly as a measured target's
// post-execution validation failure (the measured path refuses on any
// non-ctx validateProducers error), never a campaign abort
// (REQ-exec-quiescence). Callers check ctx first; a truly
// campaign-fatal cause fails the run as N refusals, exactly as on the
// measured side. The reason claims drift only where the error
// establishes it — a view-changed failure; anything else passes
// verbatim, the measured path's own attribution discipline (the
// residue clause's misattribution class).
func serveCheckRefusal(err error) string {
	if errors.Is(err, gofresh.ErrViewChanged) {
		return "drift: the tree moved past the run-start view capture: " + err.Error()
	}
	return err.Error()
}

// commitFinding delivers one finished finding to the caller's incremental
// commit callback. The finding's capture commit was read at stamp time
// (stampProvenance), so the record already names the repository state its
// evidence was validated against — ref motion since is not grounds to
// discard completed evidence, and content drift was refused target-locally
// before the finding existed: measured paths validate their producers from
// disk before stamping, and serve paths re-observe the view at their own
// evidence checks (REQ-exec-quiescence,
// REQ-exec-cancellation).
func commitFinding(ctx context.Context, commit func(Finding) error, f Finding) error {
	if commit == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit(f)
}

func reportPreparation(callback func(PreparationEvent), event PreparationEvent) {
	if callback != nil {
		callback(event)
	}
}

// Confirmation stride gating (REQ-exec-attribution): after
// confirmStreak consecutive reproductions within one target's window,
// further kills confirm at every confirmStrideth candidate; any flip
// restores full confirmation retroactively. The constants realize the
// contract's "run of consecutive reproductions" and "fixed
// deterministic stride" — the spec deliberately leaves the numbers
// code-side (nothing persisted or wire-visible depends on them).
const (
	confirmStreak = 3
	confirmStride = 4
)

// confirmationGate is the per-target, per-window stride gate over
// serial kill confirmation. Deterministic by construction: its inputs
// are the candidate-ordered reproduction results, never worker timing.
// A volatile gate (unverifiable evidence in the window) never samples.
type confirmationGate struct {
	volatile    bool
	streak      int
	sinceSample int
	flipped     bool
}

// armed reports whether the gate confirms every kill: volatile
// evidence, a flip, or the opening streak. One predicate serves both
// the confirmation decision and the advisory mode label — two copies
// would let the label lie about the gate.
func (g *confirmationGate) armed() bool {
	return g.volatile || g.flipped || g.streak < confirmStreak
}

// confirmNow reports whether the next kill confirms serially. An
// armed gate always confirms; otherwise every confirmStrideth kill
// confirms and the rest stride-skip.
func (g *confirmationGate) confirmNow() bool {
	if g.armed() {
		return true
	}
	g.sinceSample++
	if g.sinceSample >= confirmStride {
		g.sinceSample = 0
		return true
	}
	return false
}

// confirmOutcome classifies one serial confirmation as gate evidence:
// a reproduction extends the streak, a flip is a demonstrated
// collision, and an inconclusive result — a serial timeout, excluded
// from confirmation in both directions — is no evidence either way.
type confirmOutcome int

const (
	confirmReproduced confirmOutcome = iota
	confirmFlipped
	confirmInconclusive
)

// observe records a serial confirmation's evidence and reports whether
// a flip just demanded retroactive confirmation of stride-skipped
// kills.
func (g *confirmationGate) observe(outcome confirmOutcome) (retroactive bool) {
	switch outcome {
	case confirmReproduced:
		g.streak++
		return false
	case confirmInconclusive:
		return false
	}
	first := !g.flipped
	g.flipped = true
	return first
}

// reportConfirmationFlip emits the loud demotion event: a provisional
// kill the serial re-run re-scored as a survivor names its mutant and
// withdrawn killer on every advisory face — a demotion is never
// silent (REQ-exec-run-status's confirmation-flip class).
func reportConfirmationFlip(executing func(ExecutionEvent), symbol, position, withdrawnKiller string, targetIndex, targetCount int) {
	reportExecuting(executing, ExecutionEvent{
		Phase:        "confirmation-flip",
		TargetIndex:  targetIndex,
		TargetCount:  targetCount,
		Symbol:       symbol,
		FlipPosition: position,
		FlipKiller:   withdrawnKiller,
	})
}

func reportExecuting(callback func(ExecutionEvent), event ExecutionEvent) {
	if callback != nil {
		callback(event)
	}
}

func mergeFindingObservations(root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	return mergeFindingObservationsContext(context.Background(), root, env, states...)
}

func mergeFindingObservationsContext(ctx context.Context, root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, err
	}
	// The finding union is judged under the oracle evidence env - the
	// injected width included - because its children were ingested under
	// it; a raw-env merge reads a width-reading oracle's records as
	// moved and degrades the union (REQ-exec-oracle-parallelism).
	// Idempotent when the caller already passes the evidence env.
	env = engine.OracleEvidenceEnv(env)
	state, err := runtimeinput.MergeEnv(root, env, states...)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return state, nil
	}
	process := fmt.Sprintf("gomutant-finding-merge-%d", findingObservationSequence.Add(1))
	reason := "runtime input observations could not be merged for reuse: " + err.Error()
	// Best-effort naming: the first pair of observations whose path sets
	// differ names the narrowest place to look - the mover, not the
	// bracket root (REQ-result-inspection). A digest-only conflict
	// yields no path delta and the underlying error stands alone.
	for i := 1; i < len(states); i++ {
		if delta := manifestPathDelta(states[0].Manifest, states[i].Manifest, root); len(delta) > 0 {
			reason += "; diverging inputs: " + cappedNameList(delta, "inputs")
			break
		}
	}
	result, incompleteErr := runtimeinput.IncompleteEnv(root, process, reason, env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	for _, input := range states {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if input.Manifest == "" {
			continue
		}
		merged, mergeErr := runtimeinput.MergeEnv(root, env, result, input)
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

// manifestInterner shares one backing string among a run's identical retained
// observation manifests. The digest is a content hash over exactly the entries
// the canonical encoding serializes, so equal digests name byte-identical
// manifest strings and the sharing is observationally invisible; what it
// removes is candidates-times-manifest retention, which has taken a measuring
// process to tens of gigabytes on document-heavy oracles whose mutants observe
// the same input sets.
type manifestInterner struct {
	mu       sync.Mutex
	byDigest map[string]string
}

func (in *manifestInterner) intern(o runtimeinput.Observation) runtimeinput.Observation {
	if o.Manifest == "" || o.Digest == "" {
		return o
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if canonical, ok := in.byDigest[o.Digest]; ok {
		o.Manifest = canonical
		return o
	}
	in.byDigest[o.Digest] = o.Manifest
	return o
}

// completedObservationUnion unions the finding-wide baseline observations with
// the completed candidate observations. A candidate whose process could not
// prove its runtime evidence sound is excluded from the union and returned as
// explicit candidate evidence carrying its incomplete-process reason and
// measured disposition instead (REQ-exec-observation; candidate evidence,
// REQ-result-record). Baseline observations are always finding-wide: an
// incomplete baseline observation leaves the union — and so the finding —
// unverifiable. A non-nil flagged set restricts the walk to those candidate
// indexes (the re-execution splice); nil walks every runnable candidate.
func completedObservationUnion(ctx context.Context, root string, env []string, baselines []runtimeinput.Observation, candidates []engine.Candidate, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, flagged map[int]bool) (runtimeinput.Observation, []CandidateEvidence, error) {
	states := append([]runtimeinput.Observation(nil), baselines...)
	var evidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, nil, err
		}
		if flagged != nil && !flagged[mi] {
			continue
		}
		if _, runnable := candidate.Mutant(); !runnable {
			continue
		}
		if reason := incompletes[mi]; reason != "" {
			evidence = append(evidence, CandidateEvidence{
				Position:    candidate.Position,
				Operator:    candidate.Operator,
				Reason:      reason,
				Disposition: outcomeDisposition(outcomes[mi]),
			})
			continue
		}
		states = append(states, observations[mi])
	}
	union, err := mergeFindingObservationsContext(ctx, root, env, states...)
	if err != nil {
		return runtimeinput.Observation{}, nil, err
	}
	return union, evidence, nil
}

func outcomeDisposition(outcome engine.MutantOutcome) string {
	switch outcome {
	case engine.MutantKilled:
		return "killed"
	case engine.MutantSurvived:
		return "survived"
	default:
		return "discarded"
	}
}

// emitOracleGuidance attributes oracle instability for a measured finding
// whose merged runtime evidence landed unverifiable under a package-derived
// oracle (REQ-exec-oracle-guidance). Targets sharing one oracle set share one
// attribution: the probes run once per set, not per finding. A budget
// extension owes the same attribution as a whole measure — its suffix
// processes are what landed the merged record unverifiable.
func (t *Tree) emitOracleGuidance(ctx context.Context, f Finding, w work, symbol string, opts Options, runEnv []string, guidanceCache map[string]oracleAttribution) error {
	if opts.Guidance == nil || !unstableForBuckets(&f, opts.Exemptions) || f.OracleExplicit {
		return nil
	}
	key := strings.Join(slices.Sorted(slices.Values(w.oracle)), "\x00")
	attr, ok := guidanceCache[key]
	if !ok {
		probed, gerr := t.probeOracleInstability(ctx, w.oracle, w.groups, opts, runEnv)
		if gerr != nil {
			return gerr
		}
		attr = probed
		guidanceCache[key] = attr
	}
	// Best-effort narrowing: module-local inputs the finding observed
	// that no completed solo probe reached (REQ-exec-oracle-guidance).
	var mutantOnly []string
	if attr.completed > 0 {
		if paths, pok := moduleRelInputs(f.TargetEvidence.RuntimeInputs, t.dir); pok {
			mutantOnly = mutantOnlyInputs(paths, attr.probedPaths)
		}
	}
	opts.Guidance(buildOracleGuidance(symbol, f.TargetEvidence.RuntimeReason, w.oracle, attr, mutantOnly))
	return nil
}

// provenancePaths assembles the source-file set provenance is computed
// over: the subject views' source files, their historical package files,
// module selection, and workspace inputs.
func (t *Tree) provenancePaths(ctx context.Context, repository repositoryState, targetView *subjectView, oracleViews []*subjectView) ([]string, error) {
	// A shaped target has no target view: provenance spans the oracle
	// views alone, plus the shape's own probed files via the shape
	// digest pin (REQ-target-structural).
	var sourceFiles []string
	if targetView != nil {
		sourceFiles = append(sourceFiles, targetView.sourceFiles...)
	}
	for _, oracleView := range oracleViews {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceFiles = append(sourceFiles, oracleView.sourceFiles...)
	}
	historical, err := repository.historicalPackageFilesContext(ctx, sourceFiles)
	if err != nil {
		return nil, err
	}
	sourceFiles = append(sourceFiles, historical...)
	sourceFiles = withModuleSelectionPaths(sourceFiles)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum")), nil
}

// stampProvenance records the current tree's provenance on a finding -
// measured, grown, drifted, or served: the capture commit, and dirty
// computed over the subject source files, their historical package
// files, module selection, workspace inputs, and runtime-input paths
// derived from the finding's own subject evidence (attached before any
// stamp on every path). Manifest identities are module-relative, so
// each subject's manifest resolves against that subject's own module
// directory - under a repository root above the module, resolving at
// the root would materialize local-but-nonexistent paths whose
// dirtiness git never reports, letting a false-clean portable row land
// (REQ-result-stale, REQ-result-layers). A record measured under dirty
// provenance becomes portable the first time it stamps with those paths
// clean, its attestations riding the promotion. An unreadable evidence
// manifest, or evidence naming a subject no view carries, stamps dirty
// terminally - later evidence goes unexamined, fail-closed.
// The stagedDrift return is non-empty exactly when a staged run's
// snapshot cannot vouch for the target - unstaged drift over its
// measured inputs, or an index re-staged mid-run - and the caller
// routes the target to its drift refusal instead of persisting a
// record (REQ-result-staged); non-staged runs always return "".
func (t *Tree) stampProvenance(ctx context.Context, repository repositoryState, targetView *subjectView, oracleViews []*subjectView, f *Finding) (stagedDrift string, err error) {
	f.StagedTree = repository.stagedTree
	sourceFiles, err := t.provenancePaths(ctx, repository, targetView, oracleViews)
	if err != nil {
		return "", err
	}
	// A shaped finding's probed files are provenance inputs the views
	// cannot name: the recipe file and the declaring files ride the
	// stamp (the synthesized probe file exists on no tree and is
	// skipped), so a dirty probed file never stamps clean provenance.
	sourceFiles = append(sourceFiles, shapedProbedFiles(t.dir, f.Shape)...)
	moduleDirs := map[string]string{}
	viewEnvs := map[string][]string{}
	if targetView != nil {
		moduleDirs[targetView.symbol] = targetView.moduleDir
		viewEnvs[targetView.symbol] = targetView.env
	}
	for _, oracleView := range oracleViews {
		moduleDirs[oracleView.symbol] = oracleView.moduleDir
		viewEnvs[oracleView.symbol] = oracleView.env
	}
	for _, evidence := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if evidence.RuntimeInputs == "" {
			continue
		}
		base := moduleDirs[evidence.Symbol]
		if base == "" {
			f.Dirty = true
			return "", nil
		}
		paths, perr := runtimeinput.Paths(evidence.RuntimeInputs, base)
		if perr != nil {
			f.Dirty = true
			return "", nil
		}
		// An identity outside the repository is not git's to vouch for:
		// dirty means git-visible drift, and the portable line already
		// keeps a record with external identities machine-local with the
		// truthful machine-local-input reason. Stamping such records
		// dirty forever would only mislabel why they cannot promote
		// (REQ-result-layers). The repository root is git's physical
		// path while a recorded identity is the literal path the run
		// opened, so a literal identity outside the root may still be an
		// alias form of an in-repo path (a working tree reached through
		// a symlink): judge the physical form git sees, and keep any
		// identity whose physical location cannot be established - form
		// divergence resolves fail-closed, never silently external.
		var unresolvable []string
		for _, p := range paths {
			if rel, rerr := filepath.Rel(repository.root, p); rerr == nil && filepath.IsLocal(rel) {
				sourceFiles = append(sourceFiles, p)
				continue
			}
			resolved, rerr := filepath.EvalSymlinks(p)
			if rerr != nil {
				// The identity itself does not resolve, but its deepest
				// resolvable ancestor decides the family: an ancestor
				// inside the repository reconstructs an in-repo path -
				// git reports drift under it (a tracked file deleted
				// behind a dead alias) - while a genuinely external
				// chain (swept oracle scratch under the temp root) is a
				// candidate for the digest vouch below.
				if ancestor, remainder, ok := deepestResolvedAncestor(p); ok {
					if rel, rerr := filepath.Rel(repository.root, ancestor); rerr == nil && filepath.IsLocal(rel) {
						// The pathspec anchors at the FIRST unresolved
						// component: git never matches an index entry
						// shallower than a pathspec, and the drift may
						// be on an intermediate tracked symlink, not
						// the leaf.
						first, _, _ := strings.Cut(remainder, string(filepath.Separator))
						sourceFiles = append(sourceFiles, filepath.Join(ancestor, first))
						continue
					}
				}
				unresolvable = append(unresolvable, p)
				continue
			}
			if rel, rerr := filepath.Rel(repository.root, resolved); rerr == nil && filepath.IsLocal(rel) {
				sourceFiles = append(sourceFiles, resolved)
			}
		}
		if len(unresolvable) > 0 {
			// An externally rooted identity that cannot be established
			// now may still be exactly as recorded: revalidation that
			// reproduces the evidence whole - state, reason, and digest,
			// the serve precheck's own comparison - proves every
			// identity unchanged since measurement (a swept
			// oracle-scratch path recorded missing is missing still),
			// and an unchanged external identity is not git-visible
			// drift. Anything less keeps the fail-closed arm
			// (REQ-result-layers, REQ-exec-oracle-scratch).
			state, serr := runtimeinput.CurrentEnvContext(ctx, evidence.RuntimeInputs, base, viewEnvs[evidence.Symbol])
			if serr != nil || !state.OK ||
				state.Unverifiable != evidence.RuntimeUnverifiable || state.Reason != evidence.RuntimeReason ||
				state.Digest != evidence.RuntimeDigest {
				sourceFiles = append(sourceFiles, unresolvable...)
			}
		}
	}
	f.Dirty, err = repository.pathsDirtyContext(ctx, sourceFiles)
	if err != nil {
		return "", err
	}
	// The capture commit is read at stamp time, not served from the
	// run-start snapshot — the record names the repository state the
	// finding's just-validated evidence is true of, so ref motion
	// between measurements changes later stamps and discards nothing —
	// and it is read AFTER the dirty judgment so the pair is atomic
	// under REQ-exec-quiescence's precondition, where the only legal
	// mid-run repository event is a commit: a clean judgment means
	// worktree, index, and HEAD agreed when status ran, and a commit
	// landing between the two reads cannot change worktree bytes, so
	// the later-read commit still carries the judged content. A mid-run
	// git failure resolves to the no-commit-provenance posture — Commit
	// empty, Dirty true, machine-local — fail-safe and target-local,
	// never a campaign abort; a staged run, whose records never persist
	// dirty, refuses the target with the failure named instead
	// (REQ-exec-quiescence, REQ-exec-cancellation, REQ-result-staged).
	if commit, cerr := repository.currentCommitContext(ctx); cerr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if repository.staged {
			return "commit provenance unavailable at stamp time; the staged snapshot cannot be attributed", nil
		}
		f.Commit, f.Dirty = "", true
	} else {
		f.Commit = commit
	}
	if repository.staged {
		if moved, merr := repository.snapshotMovedContext(ctx); merr != nil {
			return "", merr
		} else if moved {
			return "the index snapshot was re-staged mid-run; the recorded tree no longer names the measured content", nil
		}
		if f.Dirty {
			return "unstaged drift over the measured package's inputs; stage or stash it to pin the snapshot", nil
		}
	}
	return "", nil
}

// spliceGrownFinding serves a record under the oracle-growth carve-out
// (REQ-result-stale): recorded kills and discards stand, the re-executed
// survivors' fresh outcomes replace their dispositions, and the grown record
// carries the current tree's evidence for every subject — the gate proved
// the retained subjects' only movement is the inert compartment delta, and
// the delta run captured the added oracles' evidence — plus the current
// compartment ledger. The delta processes' completed union is reconciled
// over the record's persisted union exactly as the budget extension's: a
// read beyond the recorded pins preserves the grown outcome but stamps it
// explicitly non-reusable. Returns the reconciled union (for provenance)
// and the shed attestations of newly killed survivors.
func (t *Tree) spliceGrownFinding(ctx context.Context, env []string, rec Finding, w work, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string, exemptions []Exemption) (Finding, runtimeinput.Observation, []Attestation, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, w.candidates, w.growSurvivors, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, runtimeinput.Observation{}, nil, err
	}
	rec = spliced.rec
	// The grown record carries the current tree's evidence for every
	// subject — the gate proved the retained subjects' only movement is the
	// inert compartment delta, and the delta run captured the added
	// oracles' — with a divergence stamp transferred onto it.
	diverged := rec.TargetEvidence.RuntimeUnverifiable
	divergenceReason := rec.TargetEvidence.RuntimeReason
	rec.TargetEvidence = spliced.targetEvidence
	rec.OracleEvidence = spliced.oracleEvidence
	if diverged {
		rec.TargetEvidence.RuntimeUnverifiable, rec.TargetEvidence.RuntimeReason = true, divergenceReason
		for i := range rec.OracleEvidence {
			rec.OracleEvidence[i].RuntimeUnverifiable, rec.OracleEvidence[i].RuntimeReason = true, divergenceReason
		}
	}
	grown, shed, err := growFindingCounts(ctx, rec, w.candidates, w.growSurvivors, outcomes, killers, spliced.fresh, exemptions)
	return grown, spliced.union, shed, err
}

// growFindingCounts replaces each re-executed survivor's disposition with
// its fresh outcome — per operator and in the finding totals — while every
// other candidate keeps its recorded one (INV-RESULT-CANDIDATE-CONSERVATION;
// the growth serve re-executes exactly the recorded survivors). A survivor
// surviving the added tests keeps its recorded advisory bucket (upgraded by
// the caller's delta coverage probe), classifying unstable-oracle when the
// spliced evidence landed non-reusable; a newly killed attested survivor's
// attestation is shed and returned — evidence beats attestation
// (REQ-attest-survivor).
// carryAnchoredAttestations partitions prior dispositions over the
// surviving anchors (REQ-attest-survivor): kept dispositions adopt
// their survivor's site (a grandfathered pre-site disposition anchors
// by position and operator); a position-and-operator match whose site
// moved is a site shed - surfaced, never silent; a disposition whose
// survivor is gone lands in gone, the caller's existing shed
// semantics.
func carryAnchoredAttestations(prior []Attestation, survivors []Survivor) (kept []Attestation, siteSheds []AttestationShed, gone []Attestation) {
	siteOf := make(map[survivorKey]string, len(survivors))
	open := make(map[survivorKey]bool, len(survivors))
	for _, s := range survivors {
		key := survivorKey{s.Position, s.Operator}
		open[key] = true
		siteOf[key] = s.Site
	}
	for _, a := range prior {
		key := survivorKey{a.Position, a.Operator}
		if !open[key] {
			gone = append(gone, a)
			continue
		}
		if a.Site != "" && a.Site != siteOf[key] {
			siteSheds = append(siteSheds, AttestationShed{
				Position: a.Position,
				Operator: a.Operator,
				Reason:   "site content changed under the position - the surviving mutant is not the attested one",
			})
			continue
		}
		a.Site = siteOf[key]
		kept = append(kept, a)
	}
	return kept, siteSheds, gone
}

func growFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, survivors map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence, exemptions []Exemption) (Finding, []Attestation, error) {
	// Kill attribution is maintained only over a record that carries it
	// completely; a record predating attribution stays without one and
	// re-measures whole under the killer-drift carve-out
	// (REQ-core-attributed-kills, REQ-result-stale).
	killsComplete := len(rec.Kills) == rec.Killed
	kills := append([]Kill(nil), rec.Kills...)
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	priorBuckets := make(map[survivorKey]string, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorBuckets[survivorKey{survivor.Position, survivor.Operator}] = survivor.Execution
	}
	stamp := unstableForBuckets(&rec, exemptions)
	var stillSurviving []Survivor
	for mi := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, nil, err
		}
		if !survivors[mi] {
			continue
		}
		candidate := candidates[mi]
		i, ok := byOperator[candidate.Operator]
		if !ok {
			return Finding{}, nil, fmt.Errorf("gomutant: grown survivor %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		applyDisposition(&operators[i], "survived", -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			execution := priorBuckets[survivorKey{candidate.Position, candidate.Operator}]
			if stamp {
				execution = "unstable-oracle"
			}
			stillSurviving = append(stillSurviving, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: execution})
		case "killed":
			if killsComplete {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
			}
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, nil, fmt.Errorf("gomutant: grown operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = stillSurviving
	rec.CandidateEvidence = freshEvidence
	open := make(map[survivorKey]bool, len(stillSurviving))
	for _, survivor := range stillSurviving {
		open[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	var shed []Attestation
	for _, attestation := range rec.Attested {
		if open[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		} else {
			shed = append(shed, attestation)
		}
	}
	siteOf := make(map[survivorKey]string, len(stillSurviving))
	for _, survivor := range stillSurviving {
		siteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	// This carry runs under a gate that pins the mutated source, so the
	// anchor is position and operator over that pinned source; it still
	// stamps (adopts) sites onto grandfathered pre-site dispositions -
	// the pre-site window closes at first contact on every carry path
	// (REQ-attest-survivor). A divergent non-empty site is left for the
	// merge layer, the divergence-surfacing authority.
	for i := range kept {
		if kept[i].Site == "" {
			kept[i].Site = siteOf[survivorKey{kept[i].Position, kept[i].Operator}]
		}
	}
	rec.Attested = kept
	return rec, shed, nil
}

// spliceDriftFinding serves a record under the killer-drift carve-out
// (REQ-result-stale): kills keyed to unmoved oracles and all discards keep
// their recorded dispositions, the re-measured candidates' fresh outcomes —
// measured against the full current oracle — replace theirs, and the drifted
// record carries the current tree's evidence for every subject plus the
// current compartment ledger, exactly the growth serve's discipline. The
// re-measure processes' completed union is reconciled over the record's
// persisted union fail-closed: a read beyond the recorded pins preserves the
// drifted outcome but stamps it explicitly non-reusable. Returns the
// reconciled union (for provenance) and the shed attestations of newly
// killed survivors.
func (t *Tree) spliceDriftFinding(ctx context.Context, env []string, rec Finding, w work, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string, exemptions []Exemption) (Finding, runtimeinput.Observation, []Attestation, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, w.candidates, w.driftRemeasure, w.baselines, w.targetView, w.oracleViews, w.currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, runtimeinput.Observation{}, nil, err
	}
	rec = spliced.rec
	diverged := rec.TargetEvidence.RuntimeUnverifiable
	divergenceReason := rec.TargetEvidence.RuntimeReason
	rec.TargetEvidence = spliced.targetEvidence
	rec.OracleEvidence = spliced.oracleEvidence
	if diverged {
		rec.TargetEvidence.RuntimeUnverifiable, rec.TargetEvidence.RuntimeReason = true, divergenceReason
		for i := range rec.OracleEvidence {
			rec.OracleEvidence[i].RuntimeUnverifiable, rec.OracleEvidence[i].RuntimeReason = true, divergenceReason
		}
	}
	driftedFinding, shed, err := driftFindingCounts(ctx, rec, w.candidates, w.driftRemeasure, outcomes, killers, spliced.fresh, exemptions)
	return driftedFinding, spliced.union, shed, err
}

// driftFindingCounts replaces each re-measured candidate's disposition with
// its fresh outcome — per operator and in the finding totals — while every
// standing candidate keeps its recorded one
// (INV-RESULT-CANDIDATE-CONSERVATION). The kill list is rebuilt in candidate
// order: standing kills carry their recorded killer, re-measured candidates
// that die again (or a re-measured survivor a moved or added test now kills)
// record their fresh killer. A re-measured candidate neither killed nor
// surviving is a recorded discard (a flagged one re-executing through the
// candidate-evidence composition). Survivors are rebuilt fresh when
// re-measured against the full current oracle, classifying
// unstable-oracle when the spliced evidence landed non-reusable; a newly
// killed attested survivor's attestation is shed and returned — evidence
// beats attestation (REQ-attest-survivor).
func driftFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, remeasured map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence, exemptions []Exemption) (Finding, []Attestation, error) {
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	recordedKills := make(map[survivorKey]string, len(rec.Kills))
	for _, kill := range rec.Kills {
		recordedKills[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	recordedSurvivors := make(map[survivorKey]Survivor, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		recordedSurvivors[survivorKey{survivor.Position, survivor.Operator}] = survivor
	}
	stamp := unstableForBuckets(&rec, exemptions)
	var survivors []Survivor
	var kills []Kill
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, nil, err
		}
		key := survivorKey{candidate.Position, candidate.Operator}
		if !remeasured[mi] {
			if killer, ok := recordedKills[key]; ok {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killer})
			}
			if prior, ok := recordedSurvivors[key]; ok {
				// A standing survivor — nothing moved, so its survival
				// stands exactly like a standing kill — carries its
				// recorded advisory bucket verbatim
				// (REQ-exec-survivor-evidence).
				survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: prior.Execution})
			}
			continue
		}
		i, ok := byOperator[candidate.Operator]
		if !ok {
			return Finding{}, nil, fmt.Errorf("gomutant: drifted candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		_, wasSurvivor := recordedSurvivors[key]
		var recordedDisposition string
		switch {
		case recordedKills[key] != "":
			recordedDisposition = "killed"
		case wasSurvivor:
			recordedDisposition = "survived"
		default:
			// Neither killed nor surviving is a recorded discard — reachable
			// only through the candidate-evidence composition, whose flagged
			// discards re-execute like the candidate-local splice's.
			recordedDisposition = "discarded"
		}
		applyDisposition(&operators[i], recordedDisposition, -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			execution := ""
			if stamp {
				execution = "unstable-oracle"
			}
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: execution})
		case "killed":
			kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, nil, fmt.Errorf("gomutant: drifted operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	if len(kills) != killed {
		// A corrupt prior — a kill attribution naming a candidate the
		// record discarded — would otherwise persist a document whose next
		// parse refuses it; aborting keeps the refusal at the source.
		return Finding{}, nil, fmt.Errorf("gomutant: drifted kill attributions (%d) do not cover the killed count (%d)", len(kills), killed)
	}
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = kills
	rec.Survivors = survivors
	rec.CandidateEvidence = freshEvidence
	open := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		open[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	var shed []Attestation
	for _, attestation := range rec.Attested {
		if open[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		} else {
			shed = append(shed, attestation)
		}
	}
	siteOf := make(map[survivorKey]string, len(survivors))
	for _, survivor := range survivors {
		siteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	// This carry runs under a gate that pins the mutated source, so the
	// anchor is position and operator over that pinned source; it still
	// stamps (adopts) sites onto grandfathered pre-site dispositions -
	// the pre-site window closes at first contact on every carry path
	// (REQ-attest-survivor). A divergent non-empty site is left for the
	// merge layer, the divergence-surfacing authority.
	for i := range kept {
		if kept[i].Site == "" {
			kept[i].Site = siteOf[survivorKey{kept[i].Position, kept[i].Operator}]
		}
	}
	rec.Attested = kept
	return rec, shed, nil
}

// oracleCoverage merges the baseline coverage probes of a work item's oracle
// groups over the target's package, cached per (package, run pattern, cover
// package) across the run; probed reports false when any group cannot be
// probed (best-effort advisory data, never a refusal).
func (t *Tree) oracleCoverage(ctx context.Context, w work, opts Options, runEnv []string, cache map[string]engine.Coverage) (engine.Coverage, bool, error) {
	coverPkg := w.targetView.subject.Package
	coverage := engine.Coverage{}
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return engine.Coverage{}, false, err
		}
		key := g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg
		got, ok := cache[key]
		if !ok {
			probed, err := engine.CoveredPositions(ctx, t.dir, g.pkgs[0], g.runRegex, coverPkg, opts.OracleTimeout, g.flags, runEnv)
			if err != nil {
				if ctx.Err() != nil {
					return engine.Coverage{}, false, ctx.Err()
				}
				return engine.Coverage{}, false, nil
			}
			got = probed
			cache[key] = got
		}
		coverage = coverage.Merge(got)
	}
	return coverage, true, nil
}

func testNoun(n int) string { return countNoun(n, "test") }

func survivorNoun(n int) string { return countNoun(n, "survivor") }

func killNoun(n int) string { return countNoun(n, "kill") }

// driftRemeasureIndexes deterministically re-identifies a drifted record's
// candidates within the regenerated set and selects the re-measure set: every
// recorded survivor when any oracle moved or the derived set grew (an added
// test may kill it; with nothing moved and nothing added, survivals stand
// exactly like kills), every kill whose recorded killer is a moved oracle,
// every timeout or package-scope kill when any oracle moved — those rest on
// the whole recorded set's behavior, which a purely grown set can only
// extend — and every candidate carrying recorded candidate evidence (the
// candidate-splice composition: flagged evidence re-executes rather than
// disqualifying the record). Kills keyed to unmoved oracles and unflagged
// discards stand. stand counts the standing kills for the decision line. Any
// re-identification failure refuses the drift serve so the whole target
// re-measures (REQ-result-stale's killer-drift carve-out).
func driftRemeasureIndexes(generation engine.Generation, rec Finding, moved, added []string) (map[int]bool, int, int, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, 0, 0, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, 0, 0, false
	}
	movedSet := make(map[string]bool, len(moved))
	for _, symbol := range moved {
		movedSet[symbol] = true
	}
	remeasure := map[int]bool{}
	flagged := make(map[int]bool, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		i, ok := byIdentity[survivorKey{evidence.Position, evidence.Operator}]
		if !ok {
			return nil, 0, 0, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, 0, 0, false
		}
		flagged[i] = true
		remeasure[i] = true
	}
	survivorAt := make(map[int]bool, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok {
			return nil, 0, 0, false
		}
		survivorAt[i] = true
		if len(moved) == 0 && len(added) == 0 {
			// No oracle observed the delta and none were added, so no
			// oracle's behavior moved: survivals stand exactly like kills
			// (a flagged survivor still re-executes through its evidence).
			continue
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, 0, 0, false
		}
		remeasure[i] = true
	}
	stand := 0
	for _, kill := range rec.Kills {
		i, ok := byIdentity[survivorKey{kill.Position, kill.Operator}]
		if !ok {
			return nil, 0, 0, false
		}
		if survivorAt[i] {
			// The identity is already a survivor's: a record naming one
			// candidate both killed and surviving is corrupt (parse refuses
			// persisted documents; an in-memory prior can still carry it).
			return nil, 0, 0, false
		}
		setWide := kill.Killer == TimeoutKiller || strings.HasPrefix(kill.Killer, PackageKillerPrefix)
		if movedSet[kill.Killer] || (setWide && len(moved) != 0) || flagged[i] {
			if _, runnable := generation.Candidates[i].Mutant(); !runnable {
				return nil, 0, 0, false
			}
			remeasure[i] = true
			continue
		}
		// No runnable check for a standing kill: it is served, not
		// executed, and deterministic regeneration over the evidence-pinned
		// source reproduces the recorded runnability.
		stand++
	}
	return remeasure, stand, len(flagged), true
}

// grownSurvivorIndexes deterministically re-identifies a grown record's
// candidates and survivors within the regenerated set: the complete count
// and selection length unchanged, every identity unique, and every recorded
// survivor re-identified and still runnable. Any failure refuses the growth
// serve so the whole target re-measures (REQ-result-stale's growth
// carve-out).
func grownSurvivorIndexes(generation engine.Generation, rec Finding) (map[int]bool, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, false
	}
	survivors := make(map[int]bool, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok {
			return nil, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, false
		}
		survivors[i] = true
	}
	return survivors, true
}

// proofAbortError names a canceled campaign's in-flight proof abort:
// the wrap carries the target and its oracle so the failure is
// actionable without re-deriving which subject's view was being built
// (REQ-exec-quiescence's legibility arm), and the join keeps the
// cancellation class inspectable when err is a stored union fault that
// predates the cancellation — the bounded retry is skipped on a
// canceled campaign, so err alone may carry no cancellation.
func proofAbortError(target string, oracle []string, err, ctxErr error) error {
	return fmt.Errorf("freshness proof for target %s (oracle %s): %w", target, strings.Join(oracle, ", "), errors.Join(err, ctxErr))
}

// movedPinAttribution names why a prior finding's pins no longer cover the
// request, via the freshness inspection — the class comes from the
// inspection, never an assumed "stale" (an unverifiable prior is not stale) —
// falling back to the caller's generic note when the inspection cannot
// attribute (REQ-result-stale's decision honesty).
func (t *Tree) movedPinAttribution(ctx context.Context, rec Finding, prebuilt *subjectViewSet, fallback string) string {
	inspection, err := t.inspectFindingStateContext(ctx, rec, prebuilt)
	if err == nil && inspection.State != FindingCurrent && inspection.Reason != "" {
		return string(inspection.State) + ": " + inspection.Reason
	}
	return fallback
}

// applyDisposition folds one disposition into an operator summary: delta +1
// scores a fresh outcome, delta -1 retracts a recorded disposition before its
// replacement is scored. Every counts fold in this file goes through it so
// the disposition vocabulary is written once.
func applyDisposition(summary *OperatorSummary, disposition string, delta int) {
	switch disposition {
	case "killed":
		summary.Killed += delta
	case "survived":
		summary.Survived += delta
	case "discarded":
		summary.Discarded += delta
	}
}

// sumOperatorTotals reconciles a finding's totals from its operator
// summaries (INV-RESULT-CANDIDATE-CONSERVATION's per-operator ↔ totals
// equations have one derivation).
func sumOperatorTotals(operators []OperatorSummary) (killed, discarded, survived int) {
	for _, summary := range operators {
		killed += summary.Killed
		discarded += summary.Discarded
		survived += summary.Survived
	}
	return killed, discarded, survived
}

// candidateIdentityIndex maps every candidate identity to its enumeration
// index, refusing a duplicate identity: deterministic re-identification of
// recorded per-candidate state is only sound when identities are unique
// (REQ-result-stale's carve-outs).
func candidateIdentityIndex(candidates []engine.Candidate) (map[survivorKey]int, bool) {
	byIdentity := make(map[survivorKey]int, len(candidates))
	for i, candidate := range candidates {
		key := survivorKey{candidate.Position, candidate.Operator}
		if _, duplicate := byIdentity[key]; duplicate {
			return nil, false
		}
		byIdentity[key] = i
	}
	return byIdentity, true
}

// countNoun counts grammatically for decision reasons.
func countNoun(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", n, singular)
}

func candidateNoun(n int) string { return countNoun(n, "candidate") }

// flaggedCandidateIndexes deterministically re-identifies a served record's
// flagged candidates and recorded survivors within the regenerated candidate
// set. A record whose identities cannot all be re-identified — a moved
// count, a colliding identity, or a flagged candidate that is no longer
// runnable — cannot be spliced and reports false, sending the whole target
// back to re-measurement (REQ-result-stale).
func flaggedCandidateIndexes(generation engine.Generation, rec Finding) (map[int]bool, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, false
	}
	for _, survivor := range rec.Survivors {
		if _, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]; !ok {
			return nil, false
		}
	}
	flagged := make(map[int]bool, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		i, ok := byIdentity[survivorKey{evidence.Position, evidence.Operator}]
		if !ok {
			return nil, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, false
		}
		flagged[i] = true
	}
	return flagged, true
}

// extendedPrefixStands reports whether a regenerated enumeration under a
// wider budget re-identifies a capped record's measured prefix: the complete
// candidate count is unchanged, the selection is strictly longer than the
// record's prefix, every candidate identity is unique, the recorded
// per-operator selected counts equal the prefix's, and every recorded
// survivor re-identifies inside the prefix. Any failure refuses the extension
// so the whole target re-measures (REQ-result-stale's budget-extension
// carve-out).
func extendedPrefixStands(generation engine.Generation, rec Finding) bool {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) <= rec.Generated {
		return false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return false
	}
	prefixOperators := map[string]int{}
	for _, candidate := range generation.Candidates[:rec.Generated] {
		prefixOperators[candidate.Operator]++
	}
	if len(prefixOperators) != len(rec.Operators) {
		return false
	}
	for _, summary := range rec.Operators {
		if prefixOperators[summary.Operator] != summary.Generated {
			return false
		}
	}
	// Attestations always name survivors (enforced at parse), so the
	// survivor walk covers them too.
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok || i >= rec.Generated {
			return false
		}
	}
	return true
}

// spliceExtendedFinding serves a capped record under a wider budget
// (REQ-mut-budget, REQ-result-stale's budget-extension carve-out): the
// measured prefix keeps its recorded outcomes, dispositions, and attestations
// while each suffix candidate's fresh outcome and evidence are appended,
// conserving candidate accounting over the merged record
// (INV-RESULT-CANDIDATE-CONSERVATION). The suffix processes' completed union
// is reconciled with the record's persisted union folded in: a suffix that
// read only inputs the record already pinned leaves the evidence untouched,
// while a read beyond the record's pins is runtime information it never
// pinned, so the extended outcome is preserved but explicitly non-reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) spliceExtendedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, from int, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string, budget int, exemptions []Exemption) (Finding, error) {
	suffix := make(map[int]bool, len(candidates)-from)
	for mi := from; mi < len(candidates); mi++ {
		suffix[mi] = true
	}
	// The recorded union folds into the suffix union before reconciliation,
	// and the extension reports a measurement, never a cached serve.
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, suffix, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, err
	}
	return extendFindingCounts(ctx, spliced.rec, candidates, from, outcomes, killers, spliced.fresh, budget, exemptions)
}

// splicedEvidence is one splice's evidence outcome: the record after
// fail-closed union reconciliation, the re-executed candidates' fresh
// evidence, the reconciled union (for provenance), and the current attach
// results — kept by the growth arm, whose gate justifies upgrading every
// subject to the current tree's evidence, and discarded by the flagged and
// extension splices, whose cached proof is never upgraded by a partial
// measurement (REQ-exec-observation).
type splicedEvidence struct {
	rec            Finding
	fresh          []CandidateEvidence
	union          runtimeinput.Observation
	targetEvidence SubjectEvidence
	oracleEvidence []SubjectEvidence
}

// spliceRecordedEvidence is the shared evidence spine of every record splice
// (the flagged-candidate re-execution, the budget extension, and the
// oracle-growth serve): union the re-executed processes' observations,
// optionally fold the record's persisted union in first (comparing "recorded
// pins plus fresh reads" against the recorded pins), reconcile against the
// record fail-closed (applySplicedUnion), attach the fresh union so
// post-execution producer validation re-establishes the observation bracket,
// and stamp the current view's compartment ledger — identical to the
// record's whenever the pins matched, and the record's conformance upgrade
// when it predates the ledger (REQ-result-record). Counts folding stays with
// each arm — replacement, append, and survivor-rescore are different truths
// over the same spine.
func (t *Tree) spliceRecordedEvidence(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, reExecuted map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, labels []string, foldRecorded, cached bool) (splicedEvidence, error) {
	union, freshEvidence, err := completedObservationUnion(ctx, t.dir, env, baselines, candidates, outcomes, observations, incompletes, reExecuted)
	if err != nil {
		return splicedEvidence{}, err
	}
	if foldRecorded {
		union, err = t.foldRecordedUnion(ctx, env, rec, targetView.moduleDir, union)
		if err != nil {
			return splicedEvidence{}, err
		}
	}
	union, rec, err = t.applySplicedUnion(ctx, env, rec, union)
	if err != nil {
		return splicedEvidence{}, err
	}
	targetEvidence, oracleEvidence, err := attachEvidence(targetView, oracleViews, union)
	if err != nil {
		return splicedEvidence{}, err
	}
	rec.CompartmentLedger = compartmentLedgerFromView(currentLedger)
	rec.Labels = append([]string(nil), labels...)
	rec.Cached = cached
	return splicedEvidence{rec: rec, fresh: freshEvidence, union: union, targetEvidence: targetEvidence, oracleEvidence: oracleEvidence}, nil
}

// foldRecordedUnion merges the served record's persisted completed-process
// union into the suffix processes' fresh union, so the extension's
// reconciliation compares "recorded pins plus suffix reads" against the
// recorded pins alone: a suffix that read only inputs the record already
// pinned folds to exactly the persisted union, while a read beyond the
// record's pins survives the fold and diverges. Adoption re-evaluates the
// persisted manifest against the current tree; a moved or unadoptable
// manifest is left out, and the resulting divergence stamps the extended
// finding non-reusable rather than serving unpinned runtime information
// (REQ-result-stale's fail-closed bound).
func (t *Tree) foldRecordedUnion(ctx context.Context, env []string, rec Finding, moduleDir string, union runtimeinput.Observation) (runtimeinput.Observation, error) {
	// Adoption re-evaluates the persisted union's digests; the record was
	// ingested under the oracle evidence env, so adopting under the raw
	// env would read a width-reading record as moved and stamp the
	// extension non-reusable (REQ-exec-oracle-parallelism).
	env = engine.OracleEvidenceEnv(env)
	adopted, adoptErr := runtimeinput.AdoptEnv(rec.TargetEvidence.RuntimeInputs, moduleDir, fmt.Sprintf("gomutant-extend-%d", findingObservationSequence.Add(1)), env)
	if adoptErr != nil {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		return union, nil
	}
	return mergeFindingObservationsContext(ctx, t.dir, env, union, adopted)
}

// extendFindingCounts appends each suffix candidate's fresh outcome to the
// record — per operator and in the finding totals — while every prefix
// candidate keeps its recorded disposition, survivor identity, and
// attestation (INV-RESULT-CANDIDATE-CONSERVATION; the prefix pins did not
// move, so its attestations ride unchanged per REQ-attest-survivor). Suffix
// survivors are appended in candidate order, the suffix run's candidate
// evidence becomes the record's (an extendable record carries none), and the
// budget, generated, and candidate counts record the merged truth.
func extendFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, from int, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence, budget int, exemptions []Exemption) (Finding, error) {
	// Prefix kill attributions carry verbatim — their pins did not move —
	// and suffix kills append theirs; a record predating attribution stays
	// without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	kills := append([]Kill(nil), rec.Kills...)
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	survivors := append([]Survivor(nil), rec.Survivors...)
	// Suffix survivors of a divergence-stamped extension classify
	// unstable-oracle at once — their measurement is what ran under unstable
	// runtime conditions — while carried prefix survivors keep their
	// recorded buckets verbatim; a verifiable extension's suffix survivors
	// stay unbucketed here and earn fresh probes afterwards
	// (REQ-exec-survivor-evidence; advisory, never a pin).
	suffixExecution := ""
	if unstableForBuckets(&rec, exemptions) {
		suffixExecution = "unstable-oracle"
	}
	for mi := from; mi < len(candidates); mi++ {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		candidate := candidates[mi]
		i, ok := byOperator[candidate.Operator]
		if !ok {
			i = len(operators)
			operators = append(operators, OperatorSummary{Operator: candidate.Operator})
			byOperator[candidate.Operator] = i
		}
		operators[i].Generated++
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: suffixExecution})
		case "killed":
			if killsComplete {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
			}
		}
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].Operator < operators[j].Operator })
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = freshEvidence
	rec.Budget = budget
	rec.Generated = len(candidates)
	// Prefix dispositions carry verbatim, additionally stamping (adopting)
	// sites onto grandfathered pre-site dispositions - the pre-site window
	// closes at first contact on every carry path (REQ-attest-survivor).
	extendSiteOf := make(map[survivorKey]string, len(survivors))
	for _, survivor := range survivors {
		extendSiteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	for i := range rec.Attested {
		if rec.Attested[i].Site == "" {
			rec.Attested[i].Site = extendSiteOf[survivorKey{rec.Attested[i].Position, rec.Attested[i].Operator}]
		}
	}
	return rec, nil
}

// spliceServedFinding serves a record whose only unverifiable runtime evidence
// is candidate-local: covered candidates keep their recorded outcomes while
// each flagged candidate's fresh outcome and evidence replace its recorded
// ones, conserving per-operator and total candidate accounting
// (REQ-result-stale, INV-RESULT-CANDIDATE-CONSERVATION). The fresh completed
// union must agree with the record's completed-process union so the spliced
// evidence covers the re-executed processes without shedding any served
// process's pinned runtime inputs; fresh observations that diverge are runtime
// information the record never pinned, so the spliced outcome is preserved but
// explicitly non-reusable (REQ-exec-observation).
func (t *Tree) spliceServedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, flagged map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, killers []string, observations []runtimeinput.Observation, incompletes []string, labels []string, exemptions []Exemption) (Finding, error) {
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, flagged, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, false, true)
	if err != nil {
		return Finding{}, err
	}
	return spliceFindingCounts(ctx, spliced.rec, candidates, flagged, outcomes, killers, spliced.fresh, exemptions)
}

// applySplicedUnion reconciles the re-executed processes' completed union with
// the served record's persisted union. An equal union leaves the record's
// evidence untouched; a diverged one — different manifest, different digest,
// or an unverifiable fresh state — folds an explicit incomplete observation
// into the union and stamps every subject's evidence with the resulting
// unverifiable state, so the spliced finding is preserved but never reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) applySplicedUnion(ctx context.Context, env []string, rec Finding, union runtimeinput.Observation) (runtimeinput.Observation, Finding, error) {
	state, err := runtimeinput.CompletedState(union)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, err
	}
	if !splicedUnionDiverged(state, rec.TargetEvidence) {
		return union, rec, nil
	}
	if !state.Unverifiable {
		divergedReason := "runtime input observations diverged from the served record's completed-process union"
		// Best-effort naming: the paths present in exactly one of the two
		// unions are the narrowest place to look (REQ-result-inspection);
		// a digest-only divergence names no path and the reason stands
		// alone.
		if delta := manifestPathDelta(rec.TargetEvidence.RuntimeInputs, state.Manifest, t.dir); len(delta) > 0 {
			divergedReason += "; diverging inputs: " + cappedNameList(delta, "inputs")
		}
		incomplete, incompleteErr := runtimeinput.IncompleteEnv(t.dir, fmt.Sprintf("gomutant-splice-%d", findingObservationSequence.Add(1)), divergedReason, env)
		if incompleteErr != nil {
			return runtimeinput.Observation{}, Finding{}, incompleteErr
		}
		if union, err = mergeFindingObservationsContext(ctx, t.dir, env, union, incomplete); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
		if state, err = runtimeinput.CompletedState(union); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
	}
	rec.TargetEvidence = withRuntimeState(rec.TargetEvidence, state)
	for i := range rec.OracleEvidence {
		rec.OracleEvidence[i] = withRuntimeState(rec.OracleEvidence[i], state)
	}
	return union, rec, nil
}

// moduleRelInputs recovers a manifest's tree-local input paths in
// tree-relative slash form regardless of the manifest's stored form:
// recorded findings persist relative-form entries while engine
// observations are absolutized before they leave the engine, and naming
// must read both. Every naming site recovers against the one tree root
// so the sets it compares share a base (a workspace member's inputs
// carry the member prefix on both sides). Relativization tries the root
// as given and symlink-resolved - whichever form the recorded entries
// carry. Best-effort - ok reports whether the manifest decoded; a
// decodable manifest with no tree-local entries returns an empty set,
// which is a statement, not an absence.
func moduleRelInputs(encoded, treeDir string) (paths []string, ok bool) {
	bases := []string{treeDir}
	if resolved, err := filepath.EvalSymlinks(treeDir); err == nil && resolved != treeDir {
		bases = append(bases, resolved)
	}
	abs, err := runtimeinput.Paths(encoded, treeDir)
	if err != nil {
		return nil, false
	}
	out := []string{}
	for _, p := range abs {
		for _, base := range bases {
			rel, rerr := filepath.Rel(base, p)
			if rerr != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			out = append(out, filepath.ToSlash(rel))
			break
		}
	}
	return out, true
}

// manifestPathDelta names the tree-local input paths present in
// exactly one of two encoded manifests, sorted; undecodable manifests
// yield nothing - naming is best-effort, the divergence stamp itself
// never depends on it.
func manifestPathDelta(prior, fresh, treeDir string) []string {
	priorPaths, priorOK := moduleRelInputs(prior, treeDir)
	freshPaths, freshOK := moduleRelInputs(fresh, treeDir)
	if !priorOK || !freshOK {
		return nil
	}
	inPrior := make(map[string]bool, len(priorPaths))
	for _, path := range priorPaths {
		inPrior[path] = true
	}
	inFresh := make(map[string]bool, len(freshPaths))
	for _, path := range freshPaths {
		inFresh[path] = true
	}
	var delta []string
	for _, path := range priorPaths {
		if !inFresh[path] {
			delta = append(delta, path)
		}
	}
	for _, path := range freshPaths {
		if !inPrior[path] {
			delta = append(delta, path)
		}
	}
	sort.Strings(delta)
	return delta
}

// splicedUnionDiverged reports whether the re-executed processes' completed
// union no longer equals the served record's persisted union. A diverged
// union makes the spliced finding explicitly non-reusable (REQ-result-stale):
// keeping it reusable would serve kills whose processes read inputs the
// record never pinned — the forbidden flattering direction.
func splicedUnionDiverged(state runtimeinput.State, prior SubjectEvidence) bool {
	return state.Unverifiable || state.Manifest != prior.RuntimeInputs || state.Digest != prior.RuntimeDigest
}

// spliceFindingCounts replaces each flagged candidate's recorded disposition
// with its fresh outcome — per operator and in the finding totals — while
// every covered candidate keeps its recorded one
// (INV-RESULT-CANDIDATE-CONSERVATION). Survivor identities are rebuilt in
// candidate order carrying their recorded advisory buckets — exact under the
// pins the serve verified — an attestation rides only a survivor that
// survives again at the same position and operator (REQ-attest-survivor),
// and the fresh candidate evidence replaces the served record's.
func spliceFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, flagged map[int]bool, outcomes []engine.MutantOutcome, killers []string, freshEvidence []CandidateEvidence, exemptions []Exemption) (Finding, error) {
	// Covered kills carry their recorded attribution, flagged candidates
	// that die again record their fresh killer; a record predating
	// attribution stays without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	recordedKills := make(map[survivorKey]string, len(rec.Kills))
	for _, kill := range rec.Kills {
		recordedKills[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	var kills []Kill
	operators := append([]OperatorSummary(nil), rec.Operators...)
	rec.Operators = operators
	byOperator := make(map[string]*OperatorSummary, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = &operators[i]
	}
	priorSurvivors := make(map[survivorKey]Survivor, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorSurvivors[survivorKey{survivor.Position, survivor.Operator}] = survivor
	}
	priorEvidence := make(map[survivorKey]CandidateEvidence, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		priorEvidence[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	freshByKey := make(map[survivorKey]CandidateEvidence, len(freshEvidence))
	for _, evidence := range freshEvidence {
		freshByKey[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	var survivors []Survivor
	var candidateEvidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		key := survivorKey{candidate.Position, candidate.Operator}
		if !flagged[mi] {
			if prior, ok := priorSurvivors[key]; ok {
				// A covered survivor carries its recorded advisory bucket
				// verbatim, like its disposition and attestation
				// (REQ-exec-survivor-evidence).
				survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: prior.Execution})
			}
			if killer, ok := recordedKills[key]; ok {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killer})
			}
			continue
		}
		summary := byOperator[candidate.Operator]
		if summary == nil {
			return Finding{}, fmt.Errorf("gomutant: spliced candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		applyDisposition(summary, priorEvidence[key].Disposition, -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(summary, disposition, 1)
		if disposition == "survived" {
			// A re-executed candidate surviving again keeps its recorded
			// bucket when it has one: the pins the serve verified are
			// exactly what makes the recorded classification still exact.
			// A flip into survival (recorded killed or discarded) has no
			// recorded bucket and stays unbucketed like a probe-refused
			// survivor. Under a divergence-stamped splice the re-executed
			// survivor classifies unstable-oracle instead — its
			// re-execution is what ran under unstable runtime conditions —
			// while covered survivors above keep their recorded buckets
			// verbatim (REQ-exec-survivor-evidence).
			execution := priorSurvivors[key].Execution
			if unstableForBuckets(&rec, exemptions) {
				execution = "unstable-oracle"
			}
			survivors = append(survivors, Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: execution})
		}
		if disposition == "killed" && killsComplete {
			kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
		}
		if entry, ok := freshByKey[key]; ok {
			candidateEvidence = append(candidateEvidence, entry)
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, fmt.Errorf("gomutant: spliced operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = candidateEvidence
	// A disposition rides only a survivor that survives again at the same
	// position and operator (REQ-attest-survivor).
	current := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		current[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	for _, attestation := range rec.Attested {
		if current[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		}
	}
	siteOf := make(map[survivorKey]string, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		siteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	// This carry runs under a gate that pins the mutated source, so the
	// anchor is position and operator over that pinned source; it still
	// stamps (adopts) sites onto grandfathered pre-site dispositions -
	// the pre-site window closes at first contact on every carry path
	// (REQ-attest-survivor). A divergent non-empty site is left for the
	// merge layer, the divergence-surfacing authority.
	for i := range kept {
		if kept[i].Site == "" {
			kept[i].Site = siteOf[survivorKey{kept[i].Position, kept[i].Operator}]
		}
	}
	rec.Attested = kept
	return rec, nil
}

func withRuntimeState(evidence SubjectEvidence, state runtimeinput.State) SubjectEvidence {
	evidence.RuntimeInputs = state.Manifest
	evidence.RuntimeDigest = state.Digest
	evidence.RuntimeUnverifiable = state.Unverifiable
	evidence.RuntimeReason = state.Reason
	return evidence
}

func summarizeOperators(candidates []engine.Candidate, outcomes []engine.MutantOutcome) []OperatorSummary {
	byOperator := map[string]*OperatorSummary{}
	operators := make([]string, 0)
	for i, candidate := range candidates {
		summary := byOperator[candidate.Operator]
		if summary == nil {
			summary = &OperatorSummary{Operator: candidate.Operator}
			byOperator[candidate.Operator] = summary
			operators = append(operators, candidate.Operator)
		}
		summary.Generated++
		applyDisposition(summary, outcomeDisposition(outcomes[i]), 1)
	}
	sort.Strings(operators)
	summaries := make([]OperatorSummary, 0, len(operators))
	for _, operator := range operators {
		summaries = append(summaries, *byOperator[operator])
	}
	return summaries
}

// attributedKill enforces the oracle as the sole arbiter (REQ-target-oracle,
// REQ-exec-attribution): a kill must name an oracle test, a timeout, or a
// probe-confirmed package failure. A named killer outside the oracle means
// the run pattern matched a test the target never claimed — an
// unattributable measurement, aborted rather than scored.
func attributedKill(killer string, oracleSet map[string]bool) error {
	if killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) {
		return nil
	}
	if oracleSet[killer] {
		return nil
	}
	return fmt.Errorf("killed by %s, which is not in the target's oracle", killer)
}

// TimeoutKiller and PackageKillerPrefix re-export the engine's kill
// attributions for callers reading finding output.
const (
	TimeoutKiller       = engine.TimeoutKiller
	PackageKillerPrefix = engine.PackageKillerPrefix
)

// windowKill names one confirmable kill: the window-local target index
// and the candidate index within it.
type windowKill struct{ target, mi int }

// confirmWindowKills walks every confirmable kill of one execution
// window — targets in order, candidates in order within each — under
// per-target stride gates sharing the window's flip signal, because the
// pool load the gate samples against was shared by every target of the
// window (REQ-exec-attribution's stride-gate clause). Evidence flows
// where it binds: a flip anywhere marks every gate flipped and confirms
// every stride-skipped kill of the whole window; serial evidence
// arriving unverifiable re-arms that target's gate volatile and
// confirms its own skips (they were sampled under a clean assumption
// the serial run just withdrew); drained kills feed the same evidence
// path, so a collision observed during a drain still un-samples the
// window. Deterministic by construction: candidate order in, explicit
// FIFO processing, no worker-timing input.
func confirmWindowKills(volatile func(target int) bool, kills []windowKill, confirm func(windowKill, string) (confirmOutcome, error), unverifiable func(windowKill) bool) error {
	gates := map[int]*confirmationGate{}
	windowFlipped := false
	var skipped []windowKill
	gateFor := func(target int) *confirmationGate {
		g := gates[target]
		if g == nil {
			g = &confirmationGate{volatile: volatile(target), flipped: windowFlipped}
			gates[target] = g
		}
		return g
	}
	takeSkips := func(target int, all bool) []windowKill {
		var taken, kept []windowKill
		for _, k := range skipped {
			if all || k.target == target {
				taken = append(taken, k)
			} else {
				kept = append(kept, k)
			}
		}
		skipped = kept
		return taken
	}
	// mode names the gate state deciding the next confirmation — the
	// advisory vocabulary the events carry (ExecutionEvent.ConfirmationMode).
	mode := func(g *confirmationGate) string {
		if g.armed() {
			return "serial-full"
		}
		return "stride-sampled"
	}
	for _, k := range kills {
		if !gateFor(k.target).confirmNow() {
			skipped = append(skipped, k)
			continue
		}
		queue := []windowKill{k}
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			outcome, err := confirm(next, mode(gateFor(next.target)))
			if err != nil {
				return err
			}
			g := gateFor(next.target)
			if !g.volatile && unverifiable(next) {
				g.volatile = true
				queue = append(queue, takeSkips(next.target, false)...)
			}
			if g.observe(outcome) && !windowFlipped {
				// Kills walk in target order, so already-walked gates
				// are never consulted again: seeding windowFlipped into
				// every later-constructed gate is the whole sibling
				// propagation.
				windowFlipped = true
				queue = append(queue, takeSkips(0, true)...)
			}
		}
	}
	return nil
}

// classifyConfirmation maps one serial confirmation's scored result to
// gate evidence: a serial timeout is excluded from confirmation in
// both directions (no evidence either way), a reproduced kill extends
// the streak, anything else is a demonstrated collision.
func classifyConfirmation(outcome engine.MutantOutcome, killer string) confirmOutcome {
	switch {
	case outcome == engine.MutantKilled && killer == engine.TimeoutKiller:
		return confirmInconclusive
	case outcome == engine.MutantKilled:
		return confirmReproduced
	case outcome == engine.MutantDiscarded:
		// A discard proves nothing either way by its own clauses —
		// noise and probe-timeout discards are not demonstrated
		// collisions.
		return confirmInconclusive
	}
	return confirmFlipped
}

// windowEvidenceVolatile reports whether a target's window carries any
// unverifiable runtime evidence — in its baselines or any completed
// mutant observation — the signature of load-sensitive inputs the
// stride gate must not sample across (REQ-exec-attribution).
func windowEvidenceVolatile(w *work, observations []runtimeinput.Observation) bool {
	for _, b := range w.baselines {
		if b.Unverifiable {
			return true
		}
	}
	for _, o := range observations {
		if o.Unverifiable {
			return true
		}
	}
	return false
}

// deepestResolvedAncestor walks p's ancestors until one resolves,
// returning the resolved ancestor and the unresolved remainder below
// it. The filesystem root always resolves, so ok is false only for
// degenerate inputs.
func deepestResolvedAncestor(p string) (ancestor, remainder string, ok bool) {
	dir, rest := filepath.Dir(p), filepath.Base(p)
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return resolved, rest, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
