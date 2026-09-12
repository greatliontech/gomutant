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

Follow-up evidence (gmdb v0.5.2, gitfs, gomutant v0.57.7): gmdb's
author put `//gofresh:pure` on `lock.ProcessStartTime`. The plan verb
then showed no unverifiable clause on the consumer's targets (the
static closure is discharged), but a bounded campaign still observed
`/proc/<pid>/stat` at run time and reported it as "unstable oracle
evidence (volatile OS input)", classing every survivor of every target
`[unstable-oracle]` and prescribing an explicit oracle that excludes
every test opening a database. So the dependency's directive lifts
the closure clause and not the observed-input clause, and the
observed input is scored as *instability*, not only as a reuse
blocker — a survivor verdict is withheld for a reason that has
nothing to do with whether the test flips. The discharge channel this
issue asks for must cover the observation bracket, and the volatile
input must not feed the unstable-oracle class (see
narrowing-exemption-and-freshness-clauses-judged-apart.md).

Lands: user decision
