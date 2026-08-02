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

Shape sketch: resolve builds one View over all targets' subjects and
reads every decision from it; capture/confirm reuse one View per tree
generation (the campaign holds one generation; a View is immutable, so
reuse is exactly the contract). Interacts with
pipeline-preparation-with-execution.md only at the seam where
preparation emits ready targets — a shared View is phase-scoped either
way.

Lands: the gomutant train — evidence-ranked first by wall-clock among
the train's items (~40% of the measured warm campaign); final ordering
is the user's call at train opening.
