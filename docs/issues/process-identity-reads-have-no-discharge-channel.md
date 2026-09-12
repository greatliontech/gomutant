# Process-identity reads (/proc/<pid>/stat) have no discharge channel

gmdb's lock liveness reads `/proc/<pid>/stat` to compare a lock
holder's start time (gmdb `internal/lock/proc_linux.go`). Every gitfs
oracle opens a gmdb database, so every record is classed
"unverifiable: volatile OS input: /proc/<pid>/stat" and stays
machine-local. The input is volatile by construction — a pid's start
time differs on every process — and behavior-irrelevant to any
verdict: it identifies the reading process, it does not vary the
subject's output.

Neither declared surface fits: `--bracket-path` refuses a path outside
the tree under a tool-excluded directory, and `--vouch` names a
dependency variable, not a file read. The only remaining lever is a
`//gofresh:pure` directive on every consumer symbol or the caller's
whole-run assertion, both of which assert far more than "this
process-identity read is fine".

Asked: a class of ephemeral process-identity inputs (`/proc/self`,
`/proc/<own pid>`, `/proc/<pid>/stat` read for liveness) recognized as
non-behavioral by construction and never a reuse blocker, or a
declarable surface for exactly that class carrying the caller's
assertion the way bracket-path does for tree paths.

Triage (2026-09-12, chunk 244's open gate): "non-behavioral by
construction" does not hold for a liveness read — the holder's start
time decides whether a lock is broken, an output the subject can vary
by — so a classification carve-out would be unsound; the sound shape
is a declared surface carrying the caller's assertion for exactly
this path class (gofresh REQ-inputs-volatile-os-roots gains the
declaration, gomutant passes it as bracket-path is passed). A new
declared surface is product scope.

Lands: user decision
