# Execution

Running a mutant answers one question: did an oracle test notice? gomutant
runs the target's oracle against each mutant and decides the outcome by a
rule strict enough that a noisy or corrupted run is refused rather than
scored.

**REQ-exec-oracle-run** (behavior): gomutant MUST run a target's oracle
against each of its mutants — in isolation, through the build overlay
([mutation.md](mutation.md)), never the whole test suite unless the oracle is
the whole suite — and report every mutant no oracle test killed as a survivor
carrying its source position and the operator that produced it. Scoping the
run to the oracle is what makes a survivor mean "the tests that vouch for this
symbol did not notice," rather than "no test anywhere noticed." An oracle
spanning packages is scoped per package — each package run with the test
pattern of its own oracle tests alone — because one union pattern would also
run a same-named non-oracle test in a sibling package, whose failure is
unattributable and aborts a sweep the per-package form completes.

**REQ-exec-attribution** (behavior): A kill MUST be one of exactly three
attributed events, enforcing REQ-core-attributed-kills: a named oracle test
that passed in a pre-measurement run of the unmutated tree reporting failure
in the mutant run's structured output; a timeout; or a
package-scope failure with no test-level event — admitted only when a
baseline probe of the unmutated tree passes, which distinguishes a
goroutine-panic-class kill from environmental noise. The probe runs even when
a hard crash truncates the structured stream before any package-level fail
event — attribution requiring a well-formed stream would make exactly the
strongest kills unmeasurable; a passing probe with no attributable package
admits the kill as an unattributed package crash. A test-attributed or
package-scope kill measured while sibling mutants could run concurrently
(worker count above one) is subject to solo confirmation after its
execution window's pool drains — no sibling mutant in flight; execution
may proceed in bounded target windows, and the isolation obligation
attaches to the drained window exactly as it did to a campaign-wide
pool — and wherever a confirmation runs, the serial execution is the
scored measurement - outcome, killer, observation, and candidate
evidence replaced wholesale - so a sibling-induced collision never
reads as a kill and a kill that does not reproduce alone scores as the
serial run says (a survivor is a finding, the anti-flattering
direction). Confirmation is stride-gated on evidence from the same
window under the same load: per target, the first
confirmations run serially in deterministic candidate order, and after
a run of consecutive reproductions every further kill confirms at a
fixed deterministic stride — worker completion order can affect none of
it. The flip signal is the execution window's, not the target's,
because every target of the window shared the pool whose load the gate
samples against: any flip — a confirmed kill not reproducing alone —
retroactively restores full confirmation for every stride-skipped kill
of every target in the window, before aggregation reads any of them,
and later targets of that window never sample; a collision observed
while draining counts exactly the same. An observed collision erases
the window's sampling residual outright. A target whose window
evidence is unverifiable — its baselines, any pool observation, or
serial evidence arriving unverifiable mid-walk, which re-arms the gate
and confirms that target's own skips — never samples (volatile inputs
are load sensitivity's signature; merged-tier unverifiability that
only aggregation can decide lands in the finding as evidence
regardless). A timeout
is excluded from confirmation in both directions: a timeout kill never
enters confirmation (re-executing one costs the full timeout again,
and the residual - a load-induced timeout - is bounded by the
caller's own oracle budget), and a serial confirmation that itself
times out is no gate evidence either way — it neither extends a streak
nor flips; a serial confirmation landing as a discard is the same
non-evidence, by the discard clauses' own "proves nothing" terms — a
flip is a serial run scoring the mutant a survivor, nothing weaker. The accepted residual of the stride gate is
exactly this: a collision kill survives only when zero sampled
confirmations anywhere in its execution window flip while an unsampled
collision exists — evidence-bounded, window-scoped, and any flip
converts the whole window back to the absolute guarantee. An unattributable failure
whose differential baseline also fails is environmental noise: never a kill,
and never a campaign abort — the one candidate records as a discard carrying
the diagnostic as candidate-local evidence and the run continues, because an
abort that discards completed measurements is reserved for corrupted
orchestration state. A baseline probe that cannot complete within the oracle
timeout proves nothing either way — the package-scope failure is not provably
mutant-caused, and calling it noise would need the probe's verdict — so the
candidate discards carrying the probe-timeout diagnostic as candidate-local
evidence and the run continues: the deadline is a per-mutant outcome, never a
campaign abort, while caller-context cancellation remains fatal as
REQ-exec-cancellation requires. A run that fails in any
other way — a build error the overlay should have prevented, a killer test
outside the oracle, output that does not parse — aborts without recording a
finding, because a corrupted measurement read as a sound one inflates kills
in the flattering direction. Under INV-RESULT-CANDIDATE-CONSERVATION in
[results.md](results.md), compiler rejection of a selected
candidate before any oracle test runs is instead a discard only after the same
package-scoped baseline passed and source/build inputs remained coherent;
generator, overlay, and malformed
output failures still abort.
Each distinct package-scoped oracle group needed by fresh targets is probed
once per run before mutant execution. A group that matches no tests or does
not pass unmutated refuses the measurement; cached findings launch no probe.
When repeated clean probes disagree on the test count or pass/fail result, the
measurement is likewise refused. Disagreement or movement confined to runtime-
input observations does not change that passing baseline result: it makes the
eventual finding explicitly unverifiable for reuse instead of suppressing the
fresh measurement.

**REQ-exec-observation** (behavior): gomutant MUST capture one independent Go
testlog observation for every mutant and oracle-baseline process it launches and
finalize completed logs against that process's package working directory. A
completed observation binds its values through an observation bracket
fingerprinted over the oracle package's directory before the process spawns
(tool-owned bookkeeping directories excluded), plus any caller-declared bracket
paths — module-relative paths (a file or a directory tree) or absolute files an
oracle legitimately reads outside its package directory, each declared with the
bracket contract's mutation-free assertion for the span, so an external fixed
fixture binds instead of sealing the observation. An absolute external
directory cannot be walked by the bracket's hashing semantics and is refused at
run start — declaring it would seal every observation, strictly worse than not
declaring — as is a declared path under a tool-excluded directory, which would
otherwise be silently uncovered; a spawn whose bracket could not
be captured finalizes as an incomplete observation carrying the capture's
stated reason, never as a completed one - the values the run read cannot bind. When
the completed states agree with one coherent current view, their deterministic
union is attached conservatively to the target and every oracle subject in the
finding together with caller-selected observation-completeness assertion and
compatible per-subject observability proof evidence. If runtime identities differ
between repeated observations or completed states move before union, gomutant
preserves the attributed fresh mutation outcomes and attaches canonical explicit
unverifiable evidence instead; a completed child whose state remains evaluable may
retain its identities, but bytes from an incomplete child are never promoted to a
completed observation merely to retain partial identities. That finding is reportable
and persistable but never reusable. A process that times out, panics, exits
before normal test-harness completion, or otherwise cannot prove its log
complete contributes an explicit unverifiable observation rather than an empty
observation assertion, and that unverifiability is candidate-local: it attaches
to the candidate the process measured, never to the finding's other candidates,
whose completed-state union remains their reuse evidence. On reuse, a finding
whose incomplete observations are all candidate-local serves its covered
candidates and re-executes exactly the unverifiable candidates under a passing
current baseline probe; identity movement or incoherence among completed states
remains finding-wide and remeasures the target. Each mutant executes exactly
once per scored measurement - a concurrent kill's serial confirmation IS the
scored execution, never a comparison between two - and the baseline validity
repeat contributes only its own scored
observation: the historical discovery-then-score double execution and its
cross-run evidence-drift comparison are retired — the pre-spawn observation
bracket binds the values each run read, which is the evidence the comparison
existed to approximate (exactly-once enforced by
`TestRunMutantExecutesExactlyOnce`). A stale or unverifiable subject
remeasures the finding; incomplete or incoherent observation is never silently
represented as reusable evidence.

Observation-completeness proof is selected only for a fresh measurement whose
baseline and mutant processes all run under this observation boundary. Cached or
historical evidence is never upgraded without rerunning the measurement. Reuse and
inspection explicitly check the persisted proof selection rather than inferring it
from the presence of a runtime manifest.
Every producer view that can receive one shared baseline observation is captured
before that baseline process starts; a completed observation is never attached to
proof evidence captured after the observed process. A launched candidate process
contributes its completed or incomplete observation even when compilation rejection
classifies the candidate as discarded rather than measured.

**REQ-exec-survivor-evidence** (behavior): A measured finding's survivors MUST
carry execution evidence bucketing why each lived: `never-executed` when the
oracle's baseline coverage never reaches the mutated position (a coverage
gap), `executed-and-passed` when the position runs and the oracle still
passes (a weak assertion or an equivalent mutant), and `unstable-oracle` when
the finding's runtime evidence is unverifiable, in which case no coverage
probe runs. Coverage is measured once per oracle group on the unmutated tree
and cached across the run's targets sharing the group and cover package —
advisory classification, never a measurement pin; an unprobeable oracle
leaves the bucket empty rather than failing a sound measurement. Served
records keep their recorded buckets; re-measurement refreshes them. A
spliced record mixes the two truthfully: survivors carried from the served
portion keep their recorded buckets verbatim — measured under the record's
own verifiable conditions, immune to a re-measured portion's divergence
stamp — while re-measured survivors are classified by what the splice
knows: the budget extension probes fresh coverage; the candidate
re-execution keeps the recorded classification, exact under the very pins
the serve verified, a flip into survival staying unbucketed like a
probe-refused survivor; and on either splice, re-measured survivors of a
non-reusable (unverifiable) spliced record classify unstable-oracle.

REQ-exec-survivor-evidence: enforced by `TestRunBucketsSurvivorExecution`,
`TestBucketSurvivorExecutionKeepsCarriedPrefixBuckets`,
`TestSpliceCountsStampReExecutedSurvivorsUnderUnverifiableEvidence`, and
`TestRunExtendsCappedFindingMeasuringOnlyTheSuffix`.

**REQ-exec-oracle-guidance** (behavior): When a fresh measurement's merged
runtime evidence lands unverifiable under a package-derived oracle — a
budget extension whose spliced record lands unverifiable included; a partial
measurement owes the same attribution — gomutant
MUST attribute the instability rather than leave the caller to bisect: each
oracle test is probed alone, tests whose solo runs produce unverifiable
evidence are named, and the report suggests narrowing to an explicit oracle of
the stable remainder ("excluding <tests> if they do not vouch for this
target"). A clean per-test sweep reports the instability as not
test-reproducible (mutant-execution induced), attributed by reason alone; a
sweep in which no probe completed claims nothing — it reports attribution
unavailable with the first probe failure. Targets sharing one oracle set share
one attribution: the probes run once per set, not per finding.
Attribution is advisory run output, never persisted to the finding, and its
probes are best-effort: a probe that errors, matches nothing, or fails skips
its test instead of aborting a run whose finding already committed. Explicit
oracles receive no attribution — the caller already chose the tests.

REQ-exec-oracle-guidance: enforced by `TestRunAttributesOracleInstability`,
`TestBuildOracleGuidanceArms`, `TestEmitOracleGuidanceGuards`, and
`TestRunExtensionDivergenceStampsAndAttributes`.

**REQ-exec-quiescence** (behavior): The caller MUST exclude source and build-input
mutation from target loading through run completion. gomutant validates captured
source views after execution and refuses ordinary drift, but, like its Gofresh
producer boundary, cannot prove that an external actor did not change and restore an
input while a compiler read it. Drift refusal is target-local: a target whose own
producer evidence no longer validates is refused with the drift named, while every
target whose evidence still validates keeps its completed finding — committed
incrementally, so a partial campaign retains its sound results. A drift-refused run
reports the refused set with a re-run hint and fails operationally (a pipeline
never reads a partial campaign as success); a transient global drift that no
surviving target's evidence reflects is still reported, never silently absorbed.
A repository HEAD move remains campaign-wide: it breaks the commit provenance
pin every finding carries — a global pin, not per-target source drift.
The same target-locality governs evidence construction: a target whose own
freshness-proof construction fails — after one bounded retry — skips with
the cause on its decision line, never overwriting a prior record and never
taking sibling targets down; a campaign-wide abort remains reserved for conditions that
invalidate every measurement (cancellation of the run itself, the HEAD
move above, a view failure spanning all targets).

**REQ-exec-property-oracles** (behavior): gomutant MUST detect recognized
property runtimes in an oracle package's imports (test variants included)
before execution and settle each detected runtime's determinism
prerequisite there, never leaving the discovery to the caller
mid-campaign — a mixed package earns every detected runtime's own
statement, because a single-winner note would state something false
either way. A rapid package
runs with its draws pinned (`-rapid.seed=1`) and its reproducer files
suppressed (`-rapid.nofailfile`) — every mutant faces the same draw
sequence, so a verdict is reproducible and the kill cache's
killing-oracle-content keying stays stable — and the run states what it
pinned, once per package and runtime, on runs that execute (a served
record was measured under its recorded regime, which the regime pin
below guarantees). The pin's cost is owned: all mutants of a campaign
share one draw sequence, so total draw coverage narrows versus
per-process random seeds, and a mutant killable only under other draws
survives deterministically — the reproducible-survivor direction is
chosen over the flaky-kill one, and the seed value is incidental, not
contract. The regime a finding's oracle ran under is a recorded
measurement pin: a record measured under other draws — a pre-regime
document included — re-measures rather than serving as reproducible.
A recognized runtime gomutant cannot pin
(gopter carries no invocation-level seed flag) earns a stated caller
prerequisite — ensure an in-suite fixed seed, or verdicts are
unreproducible — rather than a refusal: an internally pinned suite is
deterministic and indistinguishable from an unpinned one at the import
level, so refusing would misjudge sound suites; the empirical
unstable-oracle evidence machinery remains the anti-flattering backstop.
Ephemeral probes pin rapid identically, so a probe's verdict and its
runs:N per-run verdicts are reproducible; the ephemeral surface carries
no statement channel — the campaign surface owns prerequisite
statements.

**REQ-exec-ephemeral** (behavior): gomutant MUST run an ephemeral mutant — a
caller-supplied replacement of one or more existing source files, given whole,
as sequential exact-match edits to one file, or as an atomic batch of
file-scoped exact-match edits applied to the files' current
content, exercised through one build overlay against a named oracle test, the tree never touched — for the manual
mutations the operator set cannot generate (generated-data drift, resolver
seams, caller mappings). An edit that matches nothing, or matches more than
once — match starts counted overlapping, so a self-overlapping pattern with
two valid starts is ambiguous even when its non-overlapping count is one — is
refused rather than guessed: a mutation applied somewhere the
caller did not mean is a measurement of the wrong mutant. The run refuses
inputs the build would silently ignore before any process launches: a test
package that is not a loaded package import path (a flag-shaped value would
otherwise change the invocation being measured), and a replacement of a file
the loaded build does not compile — a build-constraint-excluded source or a
non-Go file — whose mutation could never be exercised and would report a
false survivor. Before running the mutant gomutant probes the named
test on the unmutated tree: a `-run` matching zero tests cannot attribute any
outcome, and a test already failing clean would fail against the mutant too
and read as a fabricated kill — the flattering direction
REQ-core-attributed-kills refuses — so either probe result refuses the run
rather than scoring it. A manual mutant that fails to build, and a baseline
probe whose test package fails to build, each refuse with the compiler's own
diagnostic in the message — manual probes are interactive evidence gathering,
so the caller repairs the edit from the compiler's reason, never from a
guess. The result reports whether the named test killed the
mutant and the attributed failing test; it is evidence for the caller to act
on, never persisted to a finding record (REQ-result-record). A kill
additionally carries its interactive evidence in the result — a bounded
excerpt of the killing test's own output anchored at its end, where Go
emits the failure block, with the dropped earlier remainder counted (a
head would bury the failure reason under run banners); a timeout
verdict's text naming the governing oracle-timeout option in both its
spellings (`oracle_timeout_sec` / `--oracle-timeout`); or a
package-scope crash's bounded text — so acting on a kill requires no
parallel re-run of the oracle. A caller may demand `runs:N` (bounded; each
run is a full oracle process): the mutant runs N times against the
once-probed baseline, the result lists every run's verdict in order with
the kill count, and the killed verdict means every run killed — N
consecutive kills split a deterministic kill from a property generator's
draw luck, and a mixed outcome reads as neither killed nor plain survival.
A baseline probe exceeding the oracle timeout refuses with an error naming
the governing oracle-timeout option in both its spellings. Interference
confirmation is a campaign discipline: an ephemeral probe is a single process
with no sibling mutants, and its advisory result carries no confirmation
pass.

Each atomic batch entry carries a canonical tree-relative slash path, a
non-empty old string, and its replacement. Every path resolves to an existing
regular file within the tree, and every old string occurs exactly once in that
file's original bytes (match starts counted overlapping, per the ambiguity
rule above). All entries resolve against the same pre-mutation file
contents; text introduced by one entry cannot satisfy another. Entries whose
ranges overlap, whose replacements are byte-identical, or whose combined
result changes no file are refused before any test process starts. The whole
batch becomes one overlay or none of it does; there is no fuzzy matching,
partial application, or worktree write.

The CLI batch input is a JSON object with exactly one `edits` array whose
entries carry string `file`, `old_string`, and `new_string` fields; unknown
document or entry fields and trailing JSON values are refused. A batch path
of `-` reads that document from standard input.

Reproducibility across runs is bounded by the oracle's own determinism: a
flaky oracle yields flaky kills, which is itself a finding about the tests.
gomutant does not promise identical survivors across runs — it promises that
an outcome it cannot attribute is refused (REQ-exec-attribution), so noise
aborts rather than scoring.

**REQ-exec-run-status** (behavior): CLI and MCP faces MUST report `loading`
before tree loading; the shared runner reports `resolving` before each target's
target and oracle resolution, `freshness` before constructing and checking that
target's subject views, `mutants` before enumerating a target that requires
measurement, and `baseline` before each package-scoped oracle group actually
probed rather than reused within the run. Resolution and freshness events
follow target order before module-batched view construction; subsequent mutant
and baseline events follow target order, with baseline events in canonical
package-group order. Worker count cannot affect the sequence's content or
order. Preparation is pipelined with execution: once a target's preparation
completes and its decision is reported, it may enter execution while later
targets still prepare, so later targets' preparation events and decisions may
follow earlier targets' execution-phase events — the preparation-and-decision
sequence itself stays deterministic, target-ordered, and worker-independent,
only its interleaving with the advisory execution-phase events is
timing-dependent. A window's serial confirmations exclude preparation probes
outright: a confirmation's scored run shares no process with any preparation
probe, so a probe's test-level side effects — an exclusive port, a file
lock — can never manufacture a false reproduction or a false flip; beyond
that exclusion the confirmation isolation obligation names sibling mutants,
none of which are in flight once the window drains, and preparation load is
ambient like any other process, covered by the stride gate's volatility
arm. The named residual is the baseline probe itself: a probe of one
package's tests may run beside executing mutants of that package — exactly
the concurrency worker parallelism already implies for a suite holding
cross-process-exclusive resources — or beside the advisory coverage and
guidance probes; a collision fails the probe
loudly and aborts the run, or skews an advisory bucket, never entering a
verdict. The CLI streams these events as they occur; the
MCP face carries them per REQ-mcp-envelope in [mcp.md](mcp.md) — streamed to
progress notifications or inline-capped, totals always exact. The CLI reports
each skipped target once, as its decision line, and aggregates skip classes
into one counted line after the summary when more than one target skipped — a
row that would repeat an already-rendered line is dropped at the source. The
MCP face's rows are data for the caller's own joins, not rendering: dedup is
a CLI concern. Advisory freshness-analysis
keep-alive events may accompany the deterministic sequence; they are
diagnostic, carry no ordering or completion guarantee, and never enter a
decision or finding. Advisory execution-phase progress events join the
same class: at each execution window boundary the run may report the
window's phase (executing, then one confirming event per serially
confirmed kill with the window's confirmation progress), the 1-based
index of the window's first measure target among those dispatched and the
count of measure targets prepared so far (growing to campaign-wide as
pipelined preparation completes), a representative symbol,
and exact candidate tallies over the targets prepared so far — counts of
selected candidates, carried and non-runnable ones included, exactly as the
decisions count them, growing to the campaign-wide totals as pipelined
preparation completes — timing-dependent by nature, outside the
deterministic sequence, never entering a decision or finding, so an
operator can read phase and progress from the log alone. Event data never enters a run
decision or finding, and run inputs are snapshotted before delivery. Callbacks
execute synchronously as trusted caller code and must return normally; their
external side effects have ordinary process semantics. An error or cancellation
may leave a rendered prefix, but never a partial finding or decision.

Before a target's own mutants execute, the run reports that target's
decision, decisions streaming in target
order: `cached` when reusable prior evidence is
served, `skipped` with the skip reason when no measurement can run, or
`measure` with the selected candidate count and one reason from `no-prior`,
`forced`, `budget`, or `stale`. Forced is reported when force overrides an
existing record; budget when the requested budget exceeds that record's
coverage; stale when another reuse pin fails. Concurrent worker completion
order never changes these decisions or the final per-target and aggregate
summary. CLI progress renders each decision before that target's mutants
execute; a target's own preparation events precede its decision, and the
decision sequence is exactly the preparation sequence's target order. CLI and MCP final results expose
the same preparation sequence, decisions, and totals. Each measured or cached
result row also states its persistence layer when the record stays
machine-local, with the disqualifying reason (REQ-result-layers in
[results.md](results.md)) — the CLI as a `machine-local:` sub-row, the MCP
face as `layer`/`layerReason` row fields — so a run whose record the store
routes to the local overlay never reads as a healthy repo-document write.
Open survivors remain
advisory and do not change successful exit semantics.

A plan-only run MUST perform the full deterministic preparation
sequence and deliver every target decision exactly as an executing run
would — mutants enumerated, candidate counts and reasons exact — then
stop: no baseline probes, no mutant executes, and nothing new persists
(a cached serve's incremental commit is suppressed alongside the final
merge; re-merging existing records was already idempotent, and the
plan-only return carries only findings complete without execution —
cached serves and skips — never a partially enumerated measure target).
The decisions are the plan: every precondition hole recorded evidence
already names — stale pins, unverifiable or unstable oracle evidence
from prior-record inspection, skip classes preparation itself decides —
surfaces mechanically before any execution budget is committed,
splitting the workflow into plan, fix preconditions, execute; a hole
only execution can discover keeps surfacing at execution, and a plan
refuses on the same tree-motion evidence (producer drift, a moved
repository HEAD) an executing run's epilogue refuses on.

Under INV-RESULT-CANDIDATE-CONSERVATION in [results.md](results.md), a measure
decision reports its selected candidate count as
`candidates`, including candidates later discarded; `budget` means the current
request needs a longer candidate prefix than the prior finding records and the
recorded prefix could not be served, with the refusal appended to the reason.
A budget extension (REQ-result-stale's budget-extension carve-out) reports
`measure` with `candidates` counting only the measured suffix and a reason
naming the served prefix (`served: prefix of N candidates stands; measuring M
more`, the candidate noun count-aware: a one-candidate prefix reads
`1 candidate`). An oracle-growth serve (REQ-result-stale's third carve-out)
reports `measure` with `candidates` counting the re-measured survivors and
the reason `served: derived oracle grew by N tests; re-measuring M survivors
against them` (count-aware nouns); its delta run builds per-package oracle
groups over only the added test names — each group earning its own baseline
probe under the ordinary per-group discipline, so a failing added test
refuses the run before any mutant executes — dispatches only the recorded
survivors, confirms kills serially like any measured run, and attributes
kills against the full current oracle set (the run pattern already bounds
execution to the added tests).

**REQ-exec-cancellation** (behavior): An interrupt, termination signal, or
caller-context cancellation, including expiry of an operator-supplied command
timeout, MUST stop package loading and every subsequent
preparation or aggregation boundary, cancel in-flight oracle processes, wait
for their cleanup, return an operational cancellation error, and commit
nothing further to the findings document. CLI and MCP runs commit each
finished target's finding incrementally — a cached serve once its pins are
proven to hold, a measured or spliced target after its post-execution source
validation — under the same document lock the final merge takes, so an
interrupted run keeps every finding committed before cancellation became
observable while an unfinished target's work is discarded whole. The final
merge of the complete result remains the authority; re-merging a committed
finding is idempotent. A finding is committed, incrementally or finally, only
while the capture commit still names repository HEAD. Preparation progress
and ordered decisions may contain only the prefix delivered before
cancellation became observable. A cancelled run never reports or persists a
partial per-target measurement.

The command timeout bounds CLI or MCP work through its result commit. Omitted,
it is unlimited on the CLI, while the MCP tools default it to 300 seconds —
below typical MCP client request deadlines — and an explicitly supplied zero
means unlimited there too. For a findings-producing run, the final atomic
findings replacement is the success linearization point: a deadline observed
before it leaves everything except already-committed finished targets
unchanged, while a deadline after it cannot roll back the committed result and
final output completes successfully. For an ephemeral
run, completion of the attributed oracle result is the equivalent success
point. The independently named oracle timeout bounds each unmutated probe and
mutant oracle process; it defaults to 60 seconds. The oracle timeout and the
oracle memory ceiling are the two resource bounds that can change mutation
attribution and therefore enter finding reuse evidence (REQ-result-record) -
a mutant near either bound dies under a tight one and survives a loose one;
changing the command timeout alone never stales a finding.

**REQ-exec-oracle-memory** (behavior): Every oracle process tree — mutant
runs, baseline probes, and ephemeral probes alike — MUST run under a
memory ceiling: a soft runtime limit (GOMEMLIMIT at ~90% of the cap, so a
legitimately large oracle collects garbage against the ceiling instead of
dying on it) plus, where the host provides one, a hard per-process cap every
process in the tree inherits (per process, not aggregate: a
multi-process tree can jointly exceed one process's cap, and two
concurrent gomutant processes each budget their own RAM share - the
halved default is the headroom for exactly that). The default derives total RAM over twice the job count, floored
at 1 GiB — a ceiling that broke in-oracle link steps would convert every
measurement into a discard — configurable per run and disablable; an
unreadable RAM total disables the derived default rather than guessing.
The derived default moves with the job count, so evidence measured
under it re-measures when jobs change — the exact-equality discipline
every attribution-bearing pin carries — while a pinned explicit
ceiling keeps evidence stable across jobs tuning. A
mutant that dies on its ceiling classifies through the same attribution
rules as any other oracle death — ordinarily a kill with its
incompleteness reason; a legitimate oracle whose baseline also dies on
the cap routes through the noise arm as ever — so a runaway allocation
becomes a contained per-mutant verdict instead of a host-wide pressure
event that evicts unrelated processes to swap.

REQ-exec-oracle-memory: enforced by
`TestOracleMemoryCeilingContainsRunawayMutant`,
`TestDefaultOracleMemoryLimit`, and `TestOracleMemoryEnv`.

Mutation execution is supported on Unix and Windows hosts, where gomutant can
own and terminate a process group or Job Object. Other host operating systems
are refused during tree loading rather than admitted with weaker descendant
cleanup semantics.
