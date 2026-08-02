# per-target views multiply the observation pass ~270× per campaign

A warm gomutant campaign on the cerebro repro (54 targets,
`internal/compute`, gofresh v0.43.4+@9, 2026-08-02) spends 178s of its
408s wall-clock in 164 back-to-back gofresh observation passes before
the first baseline, plus 106 more at capture time — 273 full passes
(~1.05s each: typed load of every mutable-local package ~0.59s, maximal
closure batch ~0.30s, graph metadata ~0.10s, dynamic-state derivation
~0.06s) for 54 targets on one quiescent tree. Measured with an
instrumented run (per-step stderr trace on gofresh's observeView; phase
JSONL in the session records of gofresh's observability-precision plan,
chunk 4).

The cost is caller-side cardinality, not gofresh waste: each gofresh
View observes afresh by contract (REQ-fresh-coherent-view — a cached
cross-view observation would let paired observations straddle an edit),
and REQ-closure-batch-equivalence exists precisely so one multi-subject
View serves every subject of a tree generation with per-subject
evidence identical to independent analysis. gomutant constructs a fresh
View per target per internal step (~3 per target during resolve, ~2 at
capture/confirm) where one View per campaign phase — the tree is
quiescent for the whole campaign — would collapse ~273 passes to a
handful, an evidence-identical ~40% wall-clock cut on the measured
campaign.

Design addendum (2026-08-03, from the fix attempt's own failures): the
union view alone collides with gofresh's view lifecycle — a View is one
producer transaction (capture, execute, AttachObservation once per
subject, Validate seals against captures), while the campaign runs one
transaction per target over shared subjects. Two contract refusals
surfaced live: ErrViewSealed once any sibling validated, and
one-attachment-per-subject for shared oracles. The pre-validate view
READS (compartment ledgers) hoist to preparation time on the work item;
the attach/validate cycle needs a gofresh sibling-view API — derive a
per-target view over a subject subset sharing the parent's observation
(the struct-copy shape newSeededValidationView already uses
internally), fresh attach/seal state, so per-target transactions keep
their exact semantics while the observation is paid once. gofresh
lands Sibling first; gomutant's union then narrows through it.

Diagnosed (2026-08-02, code-confirmed): the decision pass is already
batched (run.go:652, one view set over all subjects), and the
multiplication lives in the target loop — run.go:788 builds a fresh
observed view set per target over {target}+oracle, plus the retry
variant at :802, once per target per phase. Fix shape: hoist one
observed view set over the union of targets and oracles before the
loop (REQ-closure-batch-equivalence makes per-subject evidence
identical, and subjectViewSet.bySymbol already serves per-subject
reads); the design-bearing constraint is failure granularity — a
per-target proof failure skips only its target today, so the union
build must keep per-symbol failure locality instead of turning a
resolution fault into a campaign abort. Interacts with
pipeline-preparation-with-execution.md only at the seam where
preparation emits ready targets — a shared view is phase-scoped either
way. Stipulator's check path shares the fix family
(check-view-cardinality there: 24 passes, 86% of the warm floor).

Lands: the gomutant train — evidence-ranked first by wall-clock among
the train's items (~40% of the measured warm campaign); final ordering
is the user's call at train opening.
