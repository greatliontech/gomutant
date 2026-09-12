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

Lands: awaiting triage
