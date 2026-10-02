# The freshness-proof phase runs far past --analysis-budget

Field report from the protodb session (2026-10-02, the standing
channel; its tracking doc is protodb's
docs/issues/2026-10-02-gomutant-derived-oracle-closure.md).

Two runs over protodb HEAD db063538 with `--analysis-budget 5s` sat in
"analysis proving oracle closure freshness (gofresh hash proof)
github.com/greatliontech/protodb/internal/db (1/1)": a `--tag dst
--targets` run (9 targets, explicit oracles of 10–30 dst suite tests)
for 23 minutes before it was killed (campaign-3d37-dst.log in that
session's scratchpad), and an untagged `--targets --force` run (28
targets) for over 10 minutes before measuring
(campaign-3d37-remeasure.log). The knob's guidance says each proof
pass is entitled to the whole budget and an unproven subject lands an
unavailable proof while the run continues (chunk 257: Options.
AnalysisBudget → gofresh.WithAnalysisBudget on every engine); what the
operator observes is the pass itself running far past 5s per subject —
either the budget does not reach the closure hash proof (a phase
distinct from the runtime-input proof pass the budget was written
against), or the cut lands but the run stalls after it. The observable
to pin: the wall time of that phase under a 5s budget over a tree whose
closure proof is minutes-class.

Lands: cross-tool train chunk 304 (its open triages this beside the
derived-oracle report: the same campaign class on the same tree; fold
or redefer at the gate).
