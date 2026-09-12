# The covering-set exemption and the freshness clauses should be judged apart

Three facts about a test decide a mutation verdict's shape, and they
are orthogonal: REACH — whether the test can execute the mutant's
extent; DETERMINISM — whether its path is a function of the tree and
pinned inputs; REUSE — whether a stored verdict may be served later.
The narrowed-survivor exemption (execution.md, REQ-exec-oracle-run)
is sound on reach alone: a test that never reaches the extent cannot
observe the mutation. Freshness clauses — a runtime input outside the
observation bracket, a volatile OS input, a startup-effect downgrade
— speak to REUSE. If any of them withholds the exemption, every
mutant on such a subject pays the full oracle for a reason that only
governs whether its record travels.

Field report (gitfs, v0.57.7, `--changed HEAD --staged`): the target
`internal/gc.lfsDigest`, a pure function over a blob's bytes reached
by the gc tests alone, priced its window at "~16m31s (0 narrowed,
53 full)" against a derived oracle of ~300 tests across 15 packages;
every survivor carried `[unstable-oracle]` and the record was
machine-local on "/proc/<pid>/stat volatile input" and "startup
effect: reaches net.Dial". The consumer could not tell from the
surfaces which of the three facts withheld the exemption: an unsound
coverage batch (the spec's stated ground), the unstable test in the
oracle (a determinism fact, since found to be a consumer defect), or
the freshness clauses.

Asked: (1) state on the run surface, per window, WHY narrowing did not
engage — which batch's coverage verdict was unsound and on what
ground — so a consumer can act on the right fact; (2) confirm that
freshness clauses never withhold the exemption, or, if they do,
decouple them: reach completeness keys the exemption, determinism
keys quarantine, freshness keys reuse.

Follow-up evidence (same consumer, later change set, dependency's
`/proc` read carrying `//gofresh:pure`): narrowing DID engage —
windows priced "(16 narrowed, 0 full)", "(8 narrowed, 0 full)", ~5 min
instead of ~16 — once the flaky consumer test was fixed, so the
withheld exemption in the first report was the unstable test, not the
freshness clauses. But every survivor of every target still carries
`[unstable-oracle]`, with the run naming the ground as the volatile
`/proc/<pid>/stat` observation: a freshness clause is being scored as
determinism evidence. Reach, determinism, and reuse are the three
facts; this run conflates the last two.

Related: own-face-gate-suite-decomposition.md (narrowing engaged but
bought little where covering ≈ suite — a different cause with the
same symptom, which is why the ground must be named).

Triage (2026-09-12, chunk 244's open gate): (2) is answered by the code
— the one covering/exempt decision (`narrowingBatches`) reads reach
facts alone (fewer than two probed batches, a batch without a coverage
verdict, an all- or none-reaching partition); no freshness fact enters
it, and instability only relabels a narrowed survivor after scoring.
Chunk 254 states that in the spec and lands (1): the estimate event
carries the ground on which narrowing did not engage, on both faces.

Lands: cross-tool train chunk 254
