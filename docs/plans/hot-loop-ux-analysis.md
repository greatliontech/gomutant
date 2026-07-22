# Hot-loop lifecycle and UX analysis (plan chunk 1.5 deliverable)

Delete-on-close analysis beside the hot-loop-ux plan; reviewed by the
user before the coding chunks run. Conclusions bind the chunks they
name; everything else is evidence.

## 1. Evidence model (decided, landed with the bump)

Bracket verdicts are the truth. A completed observation binds values
through a pre-spawn bracket over the oracle package directory; a
mid-run-mutated input seals the observation; a bracket-capture failure
degrades to incomplete, fail-closed. The self-settling-input tolerance
is gone (it was the false-reuse class). The double-probe's result and
count drift refusals stay — flaky-baseline protection no bracket
subsumes; its evidence-drift comparison is redundant with brackets and
retires in chunk 8 alongside the survivor-evidence work. External
fixed inputs (a fixture outside the module a test legitimately reads)
currently seal bracket-uncovered: caller-declared bracket paths —
stipulator's reviewed `bracket_paths` shape — join in chunk 6 with the
rest of the gofresh seam context.

## 2. Git states against records and self-cache

The findings document lives at `.gomutant/findings.json`, in the repo,
and mixes portable evidence with machine-local runtime state; the
engine writes it in place. The states that matter in a working loop:

- **Dirty worktree** — the normal case. Findings measured against
  dirty content are honest for working-tree reuse, but committing the
  findings document alongside unrelated work quietly publishes
  machine-local evidence. Conclusion (chunk 2): machine-local evidence
  moves to a user-cache overlay keyed by repo root; the committed
  document keeps only portable findings. The committability question
  ("is this artifact safe for the repo?") is exactly the parked
  findings-committability issue and falls out of the same split.
- **Branch switch / rebase mid-campaign** — the tree moves under the
  engine; the drift abort fires. Today it names neither the moved file
  nor the recovery, discards still-stable targets, and reportedly can
  exit 0 (unverified; a zero exit on an aborted campaign is a defect
  to reproduce or refute in chunk 6). Conclusion (chunk 6): the abort
  names the moved input via gofresh's attribution, keeps completed
  per-target findings, and exits nonzero with a one-line resume hint.
- **Staged index** — measuring staged content as clean evidence stays
  parked (its own issue); the cache split must simply not foreclose a
  later staged-snapshot mode (the overlay keys on content, not on
  branch names, so it does not).
- **gomutant's own residue** — `.gomutant/` shows up in changed-scope
  discovery as residue rows (chunk 3). The bracket exclusions landed
  in chunk 1 already keep it out of observation evidence.

## 3. Response envelopes at campaign scale

The MCP `run` response carries per-finding operator summaries, open
survivors, candidate evidence, preparation events, decisions, and a
summary — unbounded in the number of findings and candidates. A
campaign over a few hundred symbols multiplies every one of these
lists; the dogfooded `discover` output alone reached tens of
kilobytes with the counts buried at the end. Conclusions (chunk 9):

- Every list in a tool response gets a cap with the omitted remainder
  counted (the stipulator response-contract shape); the full document
  is already on disk and referenced by path — the response points at
  it instead of inlining what it cannot bound.
- `discover` leads with the counts and returns symbol rows only under
  an explicit detail request or a cap.
- Candidate evidence is drill-down, not default payload.

## 4. Token economy per MCP response

The consumer is an agent inside a harness paying per token. Findings
from the stipulator MCP work apply directly: one structured result
plus a one-line text summary; counts before rows; reasons on the rows
that are red, silence on the ones that are green; every remediation a
concrete next action ("re-run with --force after committing", not
prose). The `run` response's `Preparation` and `Decisions` streams are
progress data, not result data — they belong in progress
notifications (chunk 9 arms the progress-token seam with phase labels
and a heartbeat) and shrink out of the final payload.

## 5. Failure paths walked end-to-end

- **Drift abort**: see §2; chunk 6 owns attribution, retention, exit
  code, and resume hint.
- **Unverifiable findings**: today the reason strings surface raw
  gofresh reasons; chunk 5 leads findings with the record's
  unverifiable cause before open survivors, and chunk 7 attributes
  oracle instability to the responsible test with a suggested explicit
  oracle. The bracket generation makes both attributions crisper (the
  sealed input is named in the reason).
- **Goroutine-panic kills**: a baseline-clean, mutant-reproducing
  crash is a kill regardless of test attribution; unclassifiable
  mutants record unverifiable without aborting the campaign (folded
  correctness work, rides chunk 8's execution-evidence bucketing).
- **Skip noise**: targets-fed runs repeat identical skip lines;
  chunk 10 dedups by class with one methodology hint for type
  symbols, and reconciles discover/run `--changed` selection (the
  seven-vs-ten disagreement) in the same pass.

## 6. Conclusions by chunk (delta to the plan as written)

- Chunk 2 additionally resolves findings-committability (same split).
- Chunk 6 additionally: caller-declared bracket paths; reproduce or
  refute the drift-abort exit-0 report.
- Chunk 8 additionally: retire the double-probe evidence comparison;
  fold goroutine-panic kill classification.
- Chunk 10 additionally: fold discover/run --changed reconciliation
  (already noted on its issue) and the --targets help fix.
- Correctness folds with no natural chunk run as their own small
  chunks after 10: edit-uniqueness overlap counting, ephemeral input
  validation, canonical-hash literal collapse, mutant runtime
  interference (classification only; isolation is a design feature),
  spec-corpus compile diagnostics under current stipulator.
- Design-tier stays parked on stated conditions: structural mutation
  class, integration recipes, staged-snapshot mode, native MCP tasks,
  runtime-input provenance.
