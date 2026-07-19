# What "still covers" pins is undiscoverable, so callers force defensively

Lands: 6 of the active hot-loop-ux plan (legible serving semantics — the plan
chunk already carries the two-line fix this doc specifies).

## Observed

`--force` help reads "re-measure targets whose prior finding still
covers", and run output labels prior results only as served or
re-measured. Nothing on the surface says what the pin spans — the
mutated symbol's body alone, or the oracle closure too. Live instance
(tugboat, raftpb helpers): after authoring new kill-tests in the
oracle package, the caller could not tell whether the prior survivors
would be served stale (body pins unchanged) or re-measured (oracle
closure moved), and reached for `--force` defensively. The tool in
fact tracks the oracle closure (a concurrent-edit run refused with
"analysis view changed: closure for <symbol>"), so the force was
wasted work — but proving that required triggering a freshness error,
not reading the help.

## Resolution

Two lines close it: define "covers" where the flag is documented
("the pin spans the symbol body and its oracle closure; new or
changed oracle tests re-measure without --force"), and annotate each
served line with its reason ("served: body and oracle closure
unchanged") so a caller who just wrote kill-tests sees the tool
noticing them.
