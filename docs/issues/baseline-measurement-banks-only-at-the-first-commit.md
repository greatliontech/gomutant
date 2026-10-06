# A measured baseline banks only at its finding's first commit

Lands: cross-tool train chunk 302 (gomutant's record-keyed chunk — the
bank's entry keyed and deposited per oracle group at its passing probe;
slotted at 302's open gate).

## What was observed

A `gomutant run --package github.com/greatliontech/gofresh/internal/testenv
--timeout 2h --jobs 2` on 2026-10-06: the first oracle package's
baseline (gofresh's root suite) measured 29.5 minutes, the derived
budget line followed (`prepare oracle-budget … 1h57m33s`), and the run
moved on to the second package's baseline (closure, ~15–25 minutes)
and a third — all before any target had a candidate run. Had the
process died at that point (a crash, a kill, the campaign cap), every
one of those measurements would have been lost and the next run would
have paid them again.

The spec says exactly this, as a consequence of a mechanism:
REQ-result-baseline-bank — "A BASELINE deposit completes when its
finding COMMITS: the pins are the finding's own attached evidence rows
(attachment is one-shot), so a campaign killed before its first commit
banks coverage but not baselines".

## Why it is a defect

The same clause states the rule the deferral breaks: "a completed
deposit PERSISTS immediately: the bank exists to survive killed
campaigns, and persistence deferred to process exit dies with the
process" — and the coverage entries obey it, depositing batch by batch
(chunk 304). The baseline is the most expensive measurement the bank
holds and the only one still deferred past its own completion. The
train's no-wasted-work rule (2026-09-02: fail-early preparation, then an
incremental measure/persist loop per unit) is violated for the one unit
that costs the most.

The deferral's reason is mechanical, not principled: the pins a
baseline entry needs — the group's oracle-subject evidence rows — exist
before the baseline runs (the `prepare proofs` phase produces them; the
run holds the unit's evidence until its last target's terminal
disposition, chunk 311), and the bank can hold its own copy of those
rows as the coverage entry already holds its own closure pin. Waiting
for the finding's one-shot attachment couples a measurement's
persistence to an unrelated event.

## The fix

The baseline deposits at its passing probe, keyed and pinned by the
unit's proof rows (the same rows the finding later attaches; content,
so a later commit changes nothing), exactly as a coverage batch
deposits; REQ-result-baseline-bank loses its "killed before its first
commit banks … not baselines" sentence and states the per-group deposit;
a pin kills a run after its first group's baseline and before any
commit, then serves that group's baseline on the next run. The served
baseline's re-verification (its rows validated against the current
subject views through the finding-serve discipline) is unchanged.
