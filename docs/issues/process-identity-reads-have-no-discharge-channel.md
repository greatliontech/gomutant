# Process-identity reads (/proc/<pid>/stat) have no discharge channel

The reported gmdb lock liveness path reads `/proc/<pid>/stat` to compare a lock
holder's start time (gmdb `internal/lock/proc_linux.go`). Every gitfs
oracle opens a gmdb database, so every record is classed
"unverifiable: volatile OS input: /proc/<pid>/stat" and stays
machine-local in the reported campaign. The input is volatile, but not
behavior-irrelevant by construction: a holder's start time can decide whether
a lock is broken and therefore change the subject's behavior.

Neither declared surface fits: `--bracket-path` refuses a path outside
the tree under a tool-excluded directory, and `--vouch` names a
dependency variable, not a file read. The only remaining lever is a
`//gofresh:pure` directive on every consumer symbol or the caller's
whole-run assertion, both of which assert far more than "this
process-identity read is fine".

The retained request is a narrowly declared assertion for justified process-
identity use, not an automatic volatile-input carve-out. It needs an authority
home, explicit subject/root scope, provenance and revocation semantics.

A declaration must preserve Gofresh's volatile-input refusal unless the
explicit permission actually covers that use. It must not grant trust to
unrelated roots or subjects, and a static purity directive cannot stand in
for the runtime-input authority it does not carry.

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

Full-oracle@3 still runs the complete required oracle. A volatile observation
can refuse reuse without proving a flip; no declaration licenses test omissions.
Do not implement a second Gofresh input policy inside Gomutant.

Secondary constituent: tooling-redesign 6.3 separates actual flip evidence from
reuse refusal and reports incomplete advisory attribution honestly.

Lands: tooling-redesign 7.3.
