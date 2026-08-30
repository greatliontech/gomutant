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
unattributable and aborts a sweep the per-package form completes. A run
may execute a mutant's oracle as a SCHEDULE — an ordered sequence of
per-package phases, probable killers first, guided by recorded baseline
coverage over the mutant's own extent — provided the schedule is
verdict-preserving: the phases of a group partition that group's test
set exactly; a survivor verdict requires every phase to have run and
passed; a kill ends the schedule early exactly as a test failure ends a
single run; the phases of a group execute under the ONE oracle-timeout
budget the unsplit run would have applied in aggregate (the memory
ceiling is per process tree by REQ-exec-oracle-memory's own
definition), and a TIMEOUT under any narrowed phase is never a verdict
— the split's own second-process overhead is charged inside that
budget, so only the unsplit run's bound decides a timeout kill, in
either direction, via an unsplit re-measure; a test-attributed kill
from a narrowed phase pattern is
admitted only over a passing baseline of that same pattern — the shape
symmetry REQ-exec-attribution establishes on the run-regex axis, since
the full-group baseline vouches the full pattern and never a subset —
and a phase kill that baseline cannot vouch, like a split whose
individually verifiable phase observations merged unverifiable,
re-measures unsplit with the unsplit run the scored measurement;
serial confirmations execute unsplit for the same reason. Attribution
classes, the oracle of record, and the
reported oracle scope are all schedule-invariant. Coverage guides the
order under its advisory posture only (REQ-exec-survivor-evidence,
whose once-per-group bucket probe remains its own full-pattern
measurement — a union of subset runs is not that measurement): an
unavailable, unsound, or extent-less signal degrades to the unordered
run — a schedule reorders execution and never narrows it. Without an
explicit oracle timeout the campaign DERIVES each oracle group's budget
from that group's own measured baseline — the baseline probe is a
measurement of the oracle's cost on this tree under this load, run once
per group under a generous campaign measurement leash — as a multiple
with the retired 60-second default as its floor, the same derivation
the ephemeral face carries; the measurement is a passing baseline's own
wall-clock (a failing or refused baseline skips its targets and derives
nothing), every verdict-bearing process of a group — scheduled phases,
serial confirmations, structural-shaped runs alike — executes under
that group's derived budget while unmutated advisory probes run under
the leash, and an explicit timeout remains the caller's uniform
override.

**REQ-exec-attribution** (behavior): A kill MUST be one of exactly three
attributed events, enforcing REQ-core-attributed-kills: a named oracle test
that passed in a pre-measurement run of the unmutated tree reporting failure
in the mutant run's structured output; a timeout — attributed only when the
oracle's own bound fired, discriminated by that bound's own expiry cause: a
caller's command timeout expiring mid-run is a cancellation, never a kill,
because a deadline test alone cannot tell the two apart and a parent expiry
scored as a timeout kill would fabricate evidence naming a bound that never
fired; or a
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
direction). A test-attributed kill confirms KILLER-SCOPED first: the
serial run executes the killing test alone (the top-level test
function, subtests riding their parent), under the same oracle
bounds, and a killer-scoped run reporting an attributed test failure
is the scored serial measurement — admitted only over a passing
KILLER-SCOPED BASELINE of the unmutated tree, because differential
attribution is sound only when the vouching pass shares the mutant
run's shape (the symmetry rule's "differ in the overlay alone",
here on the run-regex axis): an order-dependent killer that fails
standalone regardless of the mutant must refuse the scope, never
convert a sibling-induced false kill into a confirmed one. The
scoped baseline memoizes per distinct killer for the campaign, and
any non-pass — failure, timeout, error — refuses the scope.
Anything weaker than an attributed test kill over a passing scoped
baseline — the killer passing, a timeout on either side, a
discard — scores nothing by itself: the serial run of the candidate's
whole oracle scope executes and
ITS verdict is the scored measurement, so a survivor verdict always
rests on every group the candidate executes against — the full
derived set, or the added and moved tests for a survivor narrowed
under REQ-result-stale's killer-drift carve-out, whose recorded
passes on the unmoved oracles stand (the anti-flattering direction is
untouched) — and a flip remains "a serial run scoring the mutant a
survivor" over that same scope alone; a scoped run that times out
therefore costs its oracle budget twice before the fallback
resolves, the accepted price of never scoring from a timeout. The
fallback's scoring run is the mutant's third look, one more than
the two-stage-free confirmation gave it — a look-count-sensitive
oracle carries that on its own idempotence, exactly as re-drives
do. A confirmed kill's runtime observation covers the scoped run;
the window's volatility judgment keeps its floor in the full-shape
baselines and in the pool observations as sampled before the
target's first confirmation — the sampling order, not the
replacement discipline, is what preserves the floor once scoped
observations overwrite confirmed candidates' pool rows.
Package-scope and unattributed kills have no killer to scope and
confirm against the full oracle directly.
Confirmation is stride-gated on evidence from the same
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
finalize completed logs against that process's package working directory. An
observed mutant run covers exactly one test package per process - sequential
test binaries each truncate the single per-process testlog, so a multi-package
observed request is refused with the cause rather than ingesting a capture that
silently covers only the last binary. A
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
otherwise be silently uncovered, and a declared path absent or
unhashable against a measured module's root - the base each spawn's
capture resolves against - checked before that module's first spawn: a
surface the oracle reads exists before the run, and a transient
per-test path belongs to a scratch namespace, not a bracket path; a spawn whose bracket could not
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
existed to approximate. A stale or unverifiable subject
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
carry execution evidence bucketing why each lived: `never-executed` when no
executed block of the oracle's baseline coverage intersects the mutated
node's half-open source extent (a coverage gap), `executed-and-passed` when
the extent intersects executed coverage and the oracle still passes (a weak
assertion or an equivalent mutant) — one range-shaped probe shared by every
classification pass, with a survivor row that
carries no extent answered by its anchor point alone — `overlay-bypassed` when
the finding's observed union recorded a read of a mutated file's own
on-disk path - the mutant executes through the build overlay, so a
disk-walking oracle's verdict derived from the unmutated tree and the
survivor reading is not evidence the oracle noticed nothing - and
`unstable-oracle` when
the finding's runtime evidence is unverifiable and no reviewed exemption
accepts it (REQ-result-exemptions), in which case no coverage
probe runs, and `flipped-kill` when a window run scored a kill and the
serial confirmation re-scored the mutant a survivor: the anti-flattering
scoring stands — the survivor is the verdict — and the flip rides the
RECORD, never only the event stream, carrying the withdrawn killer by
name, because the flip is itself the strongest execution evidence (the
position demonstrably executes and a named test demonstrably can fail
on it) and the survivor is oracle nondeterminism to stabilize, never a
plain coverage or assertion gap; no coverage probe runs on it, the
advisory unverifiability stamp never overwrites it, and a mutant a test
has killed is never an equivalence-attestation candidate. A drift
re-measure re-derives its re-measured survivors' buckets from the
current probe — re-judging the overlay-bypass from the current union
before classifying fresh — while standing survivors and an extension's
carried prefix are never touched; `overlay-bypassed`, `unstable-oracle`,
and `flipped-kill` are judged from evidence a coverage probe cannot see,
so no probe-derived classification ever overrides them. The overlay-bypass judgment precedes the coverage probe:
a bypassed target's coverage would bucket confidence the evidence
cannot support. Coverage is measured once per oracle group on the unmutated tree
and cached across the run's targets sharing the group and cover package —
advisory classification, never a measurement pin; an unprobeable oracle
leaves the bucket empty rather than failing a sound measurement. Every
positional surface of this requirement — the survivor anchor, the
extent, and the coverage join — speaks ON-DISK coordinates: a `//line`
directive never re-keys an anchor, a file classification, or a
coverage bucket. The cover tool emits directive-named file keys over
on-disk line numbers for such files, so profile ingest re-keys those
entries to the on-disk spelling and nothing downstream carries the
directive view. The re-keying fails closed: a rename registers only
when its on-disk side is a compiled source of the base package (the
union across the package's variants — a cgo intermediate's
back-references point at real sources and register nothing) and never
a test file, and when the directive name is neither a compiled
sibling among the sources cover instruments under that namespace
(external test variants are instrumented under their own import path,
and test files are never instrumented at all — a name cover binds
nothing to cannot be stolen from) nor claimed by more than one
on-disk file — a
colliding or contended name shares a real file's profile key and must
not steal it. The refusals are REPRESENTABLE: every claimant of a
refused key and both sides of a collision are marked
coverage-unsound, no query about an unsound file is a coverage
verdict, and the affected buckets stay empty — the best-effort
posture above, never a manufactured never-executed for covered code. Served
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

**REQ-exec-oracle-guidance** (behavior): When a fresh measurement's merged
runtime evidence lands unverifiable under a package-derived oracle — a
budget extension whose spliced record lands unverifiable included; a partial
measurement owes the same attribution — gomutant
MUST attribute the instability rather than leave the caller to bisect: each
oracle test is probed alone, tests whose solo runs produce unverifiable
evidence are named, and the report suggests narrowing to an explicit oracle of
the stable remainder ("excluding <tests> if they do not vouch for this
target"). A clean per-test sweep reports the instability as not
test-reproducible (mutant-execution induced), attributed by the reason and,
best-effort, by the module-local inputs the finding observed that no solo
probe reached — the narrowest place to look for the mutant-induced read; a
sweep in which no probe completed claims nothing — it reports attribution
unavailable with the first probe failure. Targets sharing one oracle set share
one attribution: the probes run once per set, not per finding.
Attribution is advisory run output, never persisted to the finding, and its
probes are best-effort: a probe that errors, matches nothing, or fails skips
its test instead of aborting a run whose finding already committed. Explicit
oracles receive no attribution — the caller already chose the tests.

**REQ-exec-quiescence** (behavior): The caller MUST exclude source and build-input
mutation from target loading through run completion. gomutant validates captured
source views after execution and refuses ordinary drift, but, like its Gofresh
producer boundary, cannot prove that an external actor did not change and restore an
input while a compiler read it. Mutation generation additionally pins
every loaded file's content AT THE PARSE — the digest is taken from
the same bytes the loader parsed, so no window exists between the
parse and the pin — and a generation-time re-read whose digest moved
refuses the target BEFORE any candidate is spliced: a mutation is
never generated against offsets from bytes the tree no longer holds.
A loaded file that has VANISHED at generation or resolution time is
the same drift, refused the same way — a checkout or branch switch
mid-run is drift, never a quiet skip a pipeline reads as success.
Drift refusal is target-local: a target whose own
producer evidence no longer validates is refused with the drift named, while every
target whose evidence still validates keeps its completed finding — committed
incrementally, so a partial campaign retains its sound results. A drift-refused run
reports the refused set with a re-run hint and fails operationally (a pipeline
never reads a partial campaign as success); a transient global drift that no
surviving target's evidence reflects is still reported, never silently absorbed.
A drift whose residue is untracked files written after the run began names that
provenance on the refusal it reports — the decision line for a
preparation-phase refusal, the refused-set entry for one raised after the
target's decision streamed (the once-per-target decision discipline forbids a
second row) — a mutant of filesystem-writing code (or its
oracle) can create files inside the tree during measurement, and the refusal
then reads as the run's own residue rather than operator error, self-resolving
once the residue is removed. The caller's declared own writes — its findings
document and locks — are the harness's, never measurement residue.
A repository HEAD move is not a refusal class of its own: the capture commit a
finding carries is read at stamp time — after the dirty judgment, so the pair
is atomic under this requirement's own precondition, where the only legal
mid-run repository event is a commit (a clean judgment means worktree, index,
and HEAD agreed, and a commit landing between the reads cannot change
worktree bytes) — and each
finding pins the commit its just-validated evidence is true of, so ref motion
between measurements changes later stamps and discards nothing, while a move
that changes measured content is exactly ordinary drift, refused
target-locally with completed findings retained: a measured target validates
its producers from disk immediately before stamping, and a served target's
evidence checks re-observe the view against disk when they close over the
record's runtime-input evidence — evidence the record format requires and
the serve precheck re-enforces per pair, on every serve flavor — so a
content move past the run-start view capture surfaces at the serve's own
evidence check, and any non-cancellation failure of that check is that
target's condition — refused target-locally, never returned
as a campaign failure. A mid-run
git failure at the stamp resolves to the no-commit-provenance posture
(commit omitted, `dirty=true`, machine-local), fail-safe and target-local;
a staged run, whose records never persist dirty, refuses the target with
the failure named instead.
The findings document validates commit provenance per finding, so
one campaign may truthfully record more than one capture commit.
The same target-locality governs evidence construction — symbol
resolution, oracle validation, target body-hash reads,
decision-evidence construction, and oracle baseline probes included: a
target whose own
freshness-proof construction fails — after one bounded retry — skips with
the cause on its decision line, one package's typed-load breakage
skips exactly the targets whose own closure carries it (the batched
decision views splinter per package on failure, so healthy same-module
siblings keep their views), and a failing oracle baseline — a probe
failure, an empty match, or a failing test — skips exactly the targets
whose oracles run in that package group (same package, same oracle
test set, same flags — a sibling whose oracle subset excludes the
failing test probes its own group and measures), the failing tests named so a
flaky baseline reads as itself, with the failure memoized per package
group for the campaign (siblings skip on the recorded reason without
re-probing); never overwriting a prior record and never
taking sibling targets down. A campaign-wide abort remains reserved for the one
condition that invalidates every measurement — cancellation of the run
itself.

**REQ-exec-property-oracles** (behavior): gomutant MUST detect recognized
property runtimes in an oracle package's test binary: the direct
imports (test variants included) for every runtime, and — for
flag-registering runtimes (rapid), whose pin must reach every binary
that links them — the binary's linked dependency closure regardless of
what the direct scan found, because a runtime driven solely through a
helper package registers its flags and its draws in the binary all the
same, and a direct-imports-only verdict would run it unpinned, record
the empty regime for a property-decided verdict, and let a killed
property write reproducer litter. A merely-linked non-flag runtime
(gopter) draws nothing unless a test calls it, so linkage-based
detection would mint a false prerequisite statement; it stays on the
direct-use scan. An unresolvable closure falls back to the direct scan
alone. Each detected
runtime's determinism prerequisite settles before execution, never
left to the caller
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
contract. The pin guarantees identical draws only while the recorded
failfile set is stable: rapid replays testdata/rapid failfiles
unconditionally — `-rapid.nofailfile` gates only the write — so a
pre-existing failfile is a genuine oracle input the freshness view
rightly hashes, and a failfile appearing mid-run is real drift,
rightly refused; excluding testdata/rapid from the view would forfeit
exactly that truth. The regime a finding's oracle ran under is a recorded
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
otherwise change the invocation being measured); a replacement of a file
the loaded build does not compile — a build-constraint-excluded source or a
non-Go file — whose mutation could never be exercised and would report a
false survivor; and a replacement of a file outside the named test
package's linked dependency set (the import paths `go test` compiles into
that binary) — a compiled-elsewhere file the oracle never links overlays
cleanly and every test passes, even a syntax error going unnoticed, so no
verdict exists to render: the refusal names the fact and the repair (an
oracle that links the edited package) instead of reporting a false
survivor, and an unparseable edit of such a file refuses on this ground
first, before any build could diagnose it (a linked set the derivation
cannot resolve leaves this gate standing down: a closure that does not
build refuses at the baseline probe with the compiler's own diagnostic,
this requirement's canonical framing). Before running the mutant gomutant probes the named
test on the unmutated tree: a `-run` matching zero tests cannot attribute any
outcome, and a test already failing clean would fail against the mutant too
and read as a fabricated kill — the flattering direction
REQ-core-attributed-kills refuses — so either probe result refuses the run
rather than scoring it. Without an explicit oracle timeout the mutant
budget is DERIVED from that baseline: the baseline run is itself a
measurement of the oracle's cost on this tree under this load, so it
executes under a generous measurement leash and the mutant budget
follows as a multiple with a floor — instead of a fixed knob that dies
at baseline on a loaded host and idles through most of itself on a
quiet one. The measurement can understate the mutant run's cost — a
warm-cache baseline pays no compile while the mutant run always
recompiles the mutated package inside its bound — so the floor is
never below the fixed default the derivation replaced: that relation
is contract; the particular multiple and leash values are incidental.
An explicit timeout remains the
caller's override; the result reports the effective budget and the
measured baseline either way; and a refusal or timeout kill under a
derived bound names that bound's true provenance (the leash, the
derived budget, or a command deadline that undercut them) rather than
the oracle knob that never governed it. The honest-naming duty
attaches to refusals and kills; the advisory coverage probe's bound
expiry is the recorded probe-failure posture (exercise state unknown,
the label absent), never a named refusal. A manual mutant that fails to build, and a baseline
probe whose test package fails to build, each refuse with the compiler's own
diagnostic in the message — manual probes are interactive evidence gathering,
so the caller repairs the edit from the compiler's reason, never from a
guess. The result reports whether the named test killed the
mutant and the attributed failing test; it is evidence for the caller to act
on, never persisted to a finding record (REQ-result-record). A survivor
verdict additionally names the replacement files no baseline-covered block
touches - the file is linked into the oracle's binary (an unlinked
replacement refuses at validation), yet the probed run never reached it, so
killed=false over an unexercised
replacement is not evidence the oracle noticed anything (the ephemeral twin
of the survivor-evidence buckets); the classification comes from one baseline
coverage probe run only when the verdict is not a kill (plain survival and
the mixed killed-some-runs outcome alike - both leave the false-survivor
reading open), is advisory, and is absent when
the probe fails - a probe failure never fails a sound measurement. A kill
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
events may accompany the deterministic sequence; they are
diagnostic, carry no ordering or completion guarantee, and never enter a
decision or finding. The class carries an optional payload: detail-free
events are keep-alives a consumer may throttle, while a payload-bearing
event (the per-subject analysis-unavailable provenance, the
unlisted-toolchain notice) is a distinct fact that no face may throttle,
fold, or discard at the source — transport-level advisory delivery is
unchanged — its package kept a package and its payload its own
field on every structured face. Subscribing to the class delivers both
kinds — a consumer cannot receive the keep-alives without the
diagnostics. Advisory execution-phase progress events join the
same advisory class: at each execution window boundary the run may report the
window's phase (executing, then one confirming event per serially
confirmed kill with the window's confirmation progress and the
gate's confirmation mode — serial-full while every kill confirms,
stride-sampled once the streak earns sampling, so the disarmed
stride is distinguishable from the armed one in the log — and one
confirmation-flip event per kill the serial re-run demotes — naming
the phase, symbol, mutant position, and the withdrawn provisional
killer, so a demotion is never silent on any face), the 1-based
index of the window's first measure target among those dispatched and the
count of measure targets prepared so far (growing to campaign-wide as
pipelined preparation completes), a representative symbol,
and exact candidate tallies over the targets prepared so far — counts of
selected candidates, carried and non-runnable ones included, exactly as the
decisions count them, growing to the campaign-wide totals as pipelined
preparation completes — timing-dependent by nature, outside the
deterministic sequence, never entering a decision or finding, so an
operator can read phase and progress from the log alone. The CLI
additionally renders, in the same advisory class: the requested
selection size beside the prepared-target denominator whenever the
two differ or serves and skips have offset an equality (a resumed
run's shrunken count reads as remaining work of the same request,
never a different campaign, and the context must not vanish exactly
when equality is coincidental); a cumulative progress line on a
fixed cadence — commits (cached serves included) against the
SELECTION, the served and skipped splits, candidate tallies, kills,
and elapsed time — with the cadence an operator flag defaulting on;
and a structured JSON-lines face as an option, carrying every event,
decision, per-target result row, and summary as one JSON object per
line with an event-kind field, human prose wrapped as note events —
the structured stream never loses a line the human face would show,
and no human line leaks into it. Event data never enters a run
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

**REQ-exec-plan-only** (behavior): A plan-only run MUST perform the full
deterministic preparation
sequence and deliver every target decision exactly as an executing run
would — mutants enumerated, candidate counts and reasons exact — then
stop: no baseline probes, no mutant executes, and nothing new persists
(a cached serve's incremental commit is suppressed alongside the final
merge; re-merging existing records was already idempotent, and the
plan-only return carries only findings complete without execution —
cached serves and skips — never a partially enumerated measure target).
A plan renders its own tallies and no zeroed run summary — a summary line
of zeros would claim a measurement that never happened.
The decisions are the plan: every precondition hole recorded evidence
already names — stale pins, unverifiable or unstable oracle evidence
from prior-record inspection, skip classes preparation itself decides —
surfaces mechanically before any execution budget is committed,
splitting the workflow into plan, fix preconditions, execute; a hole
only execution can discover keeps surfacing at execution, and a plan
refuses on the same tree-motion evidence (producer drift) an executing
run's epilogue refuses on.

Under INV-RESULT-CANDIDATE-CONSERVATION in [results.md](results.md), a measure
decision reports its selected candidate count as
`candidates`, including candidates later discarded; `budget` means the current
request needs a longer candidate prefix than the prior finding records and the
recorded prefix could not be served, with the refusal appended to the reason.
A budget extension (REQ-result-stale's budget-extension carve-out) reports
`measure` with `candidates` counting only the measured suffix and a reason
naming the served prefix (`served: prefix of N candidates stands; measuring M
more`, the candidate noun count-aware: a one-candidate prefix reads
`1 candidate`). A killer-drift serve (REQ-result-stale's third carve-out)
reports `measure` with `candidates` counting the re-measured candidates and
the drift reason of [results.md](results.md); its survivor-scoped
re-measure builds per-package oracle groups over only the added and moved
test names — each group earning its own baseline probe under the ordinary
per-group discipline, so a failing added test refuses the run before any
mutant executes — dispatches survivor-scoped candidates against that
narrowed set and every other re-measured candidate against the full
current groups, confirms kills serially within each candidate's own
scope, and attributes kills against the full current oracle set (the run
pattern already bounds execution to each scope's tests).

**REQ-exec-provenance** (behavior): Every tree load MUST refuse
outright — at the shared load path, so every entry on every face
inherits the guard by construction rather than per-verb discipline —
when the binary's compiled-in frontend cannot soundly judge the
toolchain that serves the load: within a major, a frontend OLDER
than the serving toolchain's language series refuses (it predates
the sources; a newer frontend reads older language under the Go 1
compatibility promise, which the declared-toolchain workflows
depend on); across majors both directions refuse; an unidentifiable
version on either side refuses — unidentifiable is not agreement.
The serving toolchain is sampled in the TARGET directory under the
SELECTION-APPLIED environment — the declared toolchain directive
honored and stray workspace variables stripped — so the witnessed
version is the one the run's loads and executions actually use,
never the tool's own cwd default. A verb that mutates state before
any load (attestation writes the findings document first) runs the
same check before its write: a skewed binary never writes, echoes
success, and then fails. The refusal names both toolchains, and on an
identified skew their language series and the rebuild direction; the
unidentifiable refusal names the versions it could not identify.

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
finding is idempotent. A finding's capture commit is read at stamp time, so
an incremental or final commit records the repository state its evidence was
validated against; repository ref motion never discards completed evidence,
and content drift keeps its target-local refusal
(REQ-exec-quiescence). Preparation progress
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
mutant oracle process — an explicit value as the caller's uniform pin, or,
omitted, the per-group derived budget REQ-exec-oracle-run defines. The
oracle timeout and the
oracle memory ceiling are the two resource bounds whose configured values
decide attribution directly and therefore pin finding reuse evidence
(REQ-result-record) -
a mutant near either bound dies under a tight one and survives a loose one;
changing the command timeout alone never stales a finding. The
inner-parallelism width is deliberately not such a pin: it reaches verdicts
through wall-clock speed and, where an oracle observably reads it, through
the recorded environment evidence (REQ-exec-oracle-parallelism).

**REQ-exec-banked-summary** (behavior): A run that reached measurement and
exits on cancellation — command-timeout expiry, signal, or abort — MUST
render a banked-state summary naming the exit cause, the count of findings
whose incremental commit RETURNED SUCCESSFULLY (with their kill and open
tallies), and the selection's disposition so far; the summary claims only
committed findings — never in-flight work and never a finding whose commit
failed — so what it reports is exactly what the findings document holds. A
run cancelled before measurement began stays silent — there is no banked
state to report — and the drift exit renders its full result rows and
summary instead, which are its banked state.

**REQ-exec-completion** (behavior): A run that returns success MUST have
dispositioned every announced target: measured and committed, served,
skipped with a recorded reason, or named in a drift error. There is no
fourth silent outcome.

**REQ-exec-truncation** (behavior): A run whose pipeline ended early
without an
error MUST fail loudly naming the unfinished targets rather than
report a truncated campaign as complete, because a truncated success
is indistinguishable from a real one unless the operator diffs the
findings document against the announced roster.

**REQ-exec-exclusivity** (behavior): A findings-producing run MUST hold an
advisory campaign lock on its findings document for its whole duration —
measurement through final merge — acquired fail-fast: a second campaign
against the same document refuses immediately, naming the holder,
instead of interleaving measurements whose merges race. The lock
releases with the holding process, so a crashed campaign never leaves a
stale lock. Short document WRITES — dispositions, lifecycle verbs,
merges — serialize under the document lock alone and remain
available while a campaign runs; inspection takes no lock at all —
reads serve from the atomically-replaced document, so they can
neither block nor be blocked. The document lock carries the same
liveness discipline: it releases with its holding process, so a
crashed writer's residue never blocks a later session, and because
the holder behind a refusal is then live by construction, the refusal
names it — a document-lock refusal never prescribes hand-removing a
marker on the supported platform. A contended write waits a bounded
budget before that refusal, and the caller's cancellation aborts the
wait at once — a client whose deadline expired is never held for the
remaining budget. Both locks are flock-based and
unix-scoped; on other hosts campaigns run without the campaign
lock's exclusivity, while the document lock stays exclusive there
through an O_EXCL marker that is not self-releasing — its refusal
names the recorded holder and the removal step. The lock files persist
by design (the flock is the lock; removing one would race two waiters
onto different inodes of one name), so inside the tool-owned document
directory the tool maintains ignore entries covering them — minted by
whichever lock first persists there, a document write with no campaign
ever run included — while a document placed in a user-owned
directory keeps that directory untouched and relies on this
documented lifecycle.

**REQ-exec-oracle-scratch** (behavior): Every oracle process tree MUST
run with its own scratch temp directory (its `TMPDIR`), removed as soon
as the process ends — with directory permissions restored before
removal, because leaked test directories can carry modes a plain
removal cannot descend. A mutant killed on timeout or by the memory
ceiling never runs its deferred cleanups; without containment its temp
directories accumulate for the campaign's lifetime, and on a
tmpfs-backed temp root that accumulation is leaked RAM compounding the
very pressure the ceiling exists to relieve. The scratch directory
lands under the operator's own temp root, so pointing a
filesystem-heavy campaign at disk-backed space is one environment
variable (`TMPDIR=/var/tmp`).

**REQ-exec-oracle-scratch-order** (behavior): The scratch sweep MUST
precede
observation finalization: the recorded evidence then captures the
swept truth — a scratch read under the declared root, absent at
ingest, admits recordless, and a read outside the admission finalizes
as a missing-path identity that revalidates stably on every later
merge and serve — where sweeping
after finalization would record content digests of files the sweep
deletes, evidence that reads moved forever. The sweep empties the
root but the root itself outlives finalization: the runtimeinput
contract admits deeper absent reads only under a root that still
resolves at ingest — an unresolvable root declares nothing — and the
emptied root is removed with the run's other ephemera once
finalization completes.

**REQ-exec-oracle-scratch-declared** (behavior): Observation ingest MUST
declare the minted scratch root to finalization as an ephemeral temp
root — the runtimeinput contract's one-identity-wide admission: the
tool created the root for this process tree and sweeps it after, so
its identity carries no observable state — because temp-tree creation
machinery stats the root to mint per-test subtrees, and an undeclared
root records as an uncovered runtime input that leaves every
temp-touching oracle's evidence machine-local and its survivors
unbucketed. The declaration names exactly the root this run minted,
never an inherited `TMPDIR` — a fail-safe direction: a missing mint
declares nothing and evidence degrades to unverifiable, rather than a
foreign temp root reading as ephemeral.

**REQ-exec-scratch-namespace** (behavior): gomutant MUST accept caller
scratch-namespace declarations - a module-relative directory and a
single-component `os.MkdirTemp`-style name pattern - and declare each
to observation ingest as a runtime-input scratch namespace, validating
the declaration's grammar when the run starts and refusing a malformed
one before any measurement. The declaration carries the caller's
assertion that absence-probes of matching names inside the directory
are no oracle's meaningful input - the namespace's one forfeited
protection, one declared namespace wide per measured module (the
directory re-roots at each module's ingest base). In-module scratch an oracle
mints and removes inside a declared namespace stops recording per-run
missing-arm identities, restoring union-equality across runs for the
serve carve-outs' persisted-union comparisons; scratch outside every
declared namespace keeps its records unchanged.

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
The derived default moves with RAM and the job count — machine
circumstance — so the recorded ceiling serves DIRECTIONALLY
(REQ-result-stale's oracle-memory clause) rather than by exact bytes,
except where the ceiling decided a verdict. A
mutant that dies on its ceiling classifies through the same attribution
rules as any other oracle death — ordinarily a kill with its
incompleteness reason; a legitimate oracle whose baseline also dies on
the cap routes through the noise arm as ever — so a runaway allocation
becomes a contained per-mutant verdict instead of a host-wide pressure
event that evicts unrelated processes to swap. A kill OR discard whose run output
— either stream: the go command reports some memory deaths on its own
stderr — carries a memory-exhaustion signature (the Go runtime's
out-of-memory fatals, ENOMEM error text) marks the record
ceiling-decided: the noise-arm discard and the in-oracle build
rejection are exactly the ceiling-authored dispositions the paragraph
above routes there. The scored verdict's disposition governs when a
serial confirmation re-runs a kill. Overcatching is the sound
direction — a test that merely prints a signature over-pins its
record — and two residuals are named: a mutant whose allocation
failure is swallowed without any signature text keeps its disposition
unattributed to memory, and a GOMEMLIMIT collection death spiral
manifests as a timeout, which the oracle-timeout pin already covers.

**REQ-exec-oracle-parallelism** (behavior): Every oracle process tree
MUST run with its inner parallelism capped so the campaign's aggregate
stays within the host: at J concurrent jobs, each tree's width — the Go
runtime's scheduler width and the go tool's package-build parallelism —
is bounded by max(1, NumCPU/J), and the cap only ever narrows: an
environment already carrying a narrower width keeps it. Without the cap
every job spawns a full-width toolchain tree — jobs × NumCPU runnable
threads, quadratic in cores at the default job count — starving the
host and its neighbor processes. Oracle trees additionally run at low
scheduling priority where the host provides one, so a saturated
campaign yields to interactive work. The width and priority are
scheduling bounds, never measurement pins — pinning a
host-geometry-derived value would machine-localize every finding — but
the injected width is part of the observed oracle environment: the
observation ingest mirror carries the effective GOMAXPROCS, so an
oracle that observably reads it records the value it actually saw as
runtime-input evidence, and exactly those width-sensitive findings
re-measure when the width moves. Every environment the evidence is
judged against — merge-time re-evaluation, serve-side revalidation,
and the analysis engines' declared producer environment — is that
same evidence environment: a raw-environment stand-in would read a
width-reading oracle's records as moved, silently degrading merges
and re-measuring serves forever, or serve stale where an ambient
value matches a record the process never reproduced. Standalone
inspection judges under the inspecting process's own width — a
run-time knob no inspection can know — so a width-reading finding may
inspect as changed yet serve on the next same-jobs run; the
divergence's direction is a spurious re-measure report, never a
spurious serve. Every other verdict path sees the
width and priority only through the wall-clock oracle timeout, exactly
as host speed and ambient load do — variance the reuse evidence
already deliberately does not pin (a finding measured on a slower or
busier host serves unchanged), with the attribution noise arm owning a
baseline that dies beside its mutant under contention.

(The go tool's package-build parallelism follows the delivered
GOMAXPROCS — `-p` defaults to it — so the environment single-sources
both dimensions; an explicit flag would override an operator's
narrower ambient bound.)

**REQ-exec-attribution-symmetry** (behavior): A mutant run and the
baseline probe that attributes its failure MUST execute under identical
resource bounds — the memory ceiling and the parallelism cap alike —
because differential attribution is sound only when the two runs differ
in the overlay alone: a mutant that exhausts a bound its baseline never
faced reads as a kill when the failure is the bound's, and a baseline
squeezed under a bound its mutant escaped converts a real kill into a
noise discard.

Mutation execution is supported on Unix and Windows hosts, where gomutant can
own and terminate a process group or Job Object. Other host operating systems
are refused during tree loading rather than admitted with weaker descendant
cleanup semantics.
