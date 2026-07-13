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
goroutine-panic-class kill from environmental noise. A run that fails in any
other way — a build error the overlay should have prevented, a killer test
outside the oracle, output that does not parse — aborts without recording a
finding, because a corrupted measurement read as a sound one inflates kills
in the flattering direction.
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
finalize completed logs against that process's package working directory. When
the completed states agree with one coherent current view, their deterministic
union is attached conservatively to the target and every oracle subject in the
finding. If runtime identities differ between repeated observations or the
completed states move before union, gomutant MUST preserve the attributed fresh
mutation outcomes, retain every observed identity whose manifest remains
evaluable under the aggregation view, and attach canonical explicit
unverifiable evidence instead; that finding is reportable and persistable but
never reusable. A process that
times out, panics, exits before normal test-harness completion, or otherwise
cannot prove its log complete likewise contributes an explicit unverifiable
observation rather than an empty observation assertion. A stale or unverifiable
subject remeasures the finding; incomplete or incoherent observation is never
silently represented as reusable evidence.

**REQ-exec-quiescence** (behavior): The caller MUST exclude source and build-input
mutation from target loading through run completion. gomutant validates captured
source views after execution and refuses ordinary drift, but, like its Gofresh
producer boundary, cannot prove that an external actor did not change and restore an
input while a compiler read it.

**REQ-exec-ephemeral** (behavior): gomutant MUST run an ephemeral mutant — a
caller-supplied replacement of one or more existing source files, given whole,
as sequential exact-match edits to one file, or as an atomic batch of
file-scoped exact-match edits applied to the files' current
content, exercised through one build overlay against a named oracle test, the tree never touched — for the manual
mutations the operator set cannot generate (generated-data drift, resolver
seams, caller mappings). An edit that matches nothing, or matches more than
once, is refused rather than guessed: a mutation applied somewhere the
caller did not mean is a measurement of the wrong mutant. Before running the mutant gomutant probes the named
test on the unmutated tree: a `-run` matching zero tests cannot attribute any
outcome, and a test already failing clean would fail against the mutant too
and read as a fabricated kill — the flattering direction
REQ-core-attributed-kills refuses — so either probe result refuses the run
rather than scoring it. The result reports whether the named test killed the
mutant and the attributed failing test; it is evidence for the caller to act
on, never persisted to a finding record (REQ-result-record).

Each atomic batch entry carries a canonical tree-relative slash path, a
non-empty old string, and its replacement. Every path resolves to an existing
regular file within the tree, and every old string occurs exactly once in that
file's original bytes. All entries resolve against the same pre-mutation file
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
probed rather than reused within the run. Target-scoped events follow target
order, baseline events follow canonical package-group order, and worker count
cannot affect the sequence. The CLI streams these events as they occur; a
successful MCP result returns the same sequence. Event data never enters a run
decision or finding, and run inputs are snapshotted before delivery. Callbacks
execute synchronously as trusted caller code and must return normally; their
external side effects have ordinary process semantics. An error or cancellation
may leave a rendered prefix, but never a partial finding or decision.

Before executing mutants, a run MUST report one target decision in target
order: `cached` when reusable prior evidence is
served, `skipped` with the skip reason when no measurement can run, or
`measure` with the generated mutant count and one reason from `no-prior`,
`forced`, `budget`, or `stale`. Forced is reported when force overrides an
existing record; budget when the requested budget exceeds that record's
coverage; stale when another reuse pin fails. Concurrent worker completion
order never changes these decisions or the final per-target and aggregate
summary. CLI progress renders the ordered decisions before mutant execution;
all preparation events precede every decision. CLI and MCP final results expose
the same preparation sequence, decisions, and totals. Open survivors remain
advisory and do not change successful exit semantics.

**REQ-exec-cancellation** (behavior): An interrupt, termination signal, or
caller-context cancellation MUST cancel in-flight oracle processes, wait for
their cleanup, return an operational cancellation error, and leave the
findings document unchanged. A cancelled run never reports or persists a
partial measurement.

Mutation execution is supported on Unix and Windows hosts, where gomutant can
own and terminate a process group or Job Object. Other host operating systems
are refused during tree loading rather than admitted with weaker descendant
cleanup semantics.
