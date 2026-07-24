# Serve-path evidence checks run per subject; the batch check shares a close

Lands: when the serve path's evidence matching checks a view's subjects
through one CheckObservedBatch call per view, or the per-subject cost is
accepted.

## Observed (warm-path fixture, gofresh observation-pass economy)

gofresh's CheckObservedBatch pays one runtime-window close per BATCH
regardless of width; gomutant's serve path checks each target and oracle
subject with its own CheckObserved call, so a view with N
runtime-carrying subjects pays N window closes where one would do. On
the fixture's warm serve (9 observation passes total), 3 are per-subject
check closes that one batched call per view would collapse to 2 - the
last consumer-side pair-class residue after the engine went close-only.

## Resolution shape

evidenceSetMatchesContext walks subjects individually to attribute the
moved pin per subject; a batched first pass (one CheckObservedBatch per
view) can answer the all-match fast path, falling back to per-subject
attribution only when the batch reports a mismatch - the attribution
path is already the slow path by construction.
