# A measured baseline banks only at its finding's first commit

Lands: tooling-redesign 6.5.

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

The historical contract described this coupling as:
REQ-result-baseline-bank — "A BASELINE deposit completes when its
finding COMMITS: the pins are the finding's own attached evidence rows
(attachment is one-shot), so a campaign killed before its first commit
banks coverage but not baselines".

## Current obligation

Passing baseline deposits still depend on the first finding commit. A passing
process alone is not a validated deposit: its own complete producing evidence,
closing validation and publication must be established independently of that
finding. Pre-baseline proof rows alone cannot supply those obligations.

The bank is shared by runs over the same tree even when they use different
findings documents. Their document locks do not serialize the shared bank's
read/merge/write cycle. Give it validated per-group deposits and shared-bank
serialization so independent completed deposits cannot overwrite one another.

Acceptance needs interruption after a passing, validated deposit but before a
finding commit, restart reuse, and concurrent deposits through distinct document
paths. Preserve refusal and cancellation behavior and the complete baseline
oracle. The coverage-schedule producer has been removed; its historical deposits
are not an implementation to restore or authority for omitting oracle tests.
