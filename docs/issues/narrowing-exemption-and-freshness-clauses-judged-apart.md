# The covering-set exemption and the freshness clauses should be judged apart

Execution reach, reproducibility and reuse applicability are distinct facts.
Reach alone does not justify a negative exemption: a mutation can influence
compilation without executing its declaration, and child execution can be absent
from a parent's profile. Current REQ-exec-oracle-run requires every required
group unsplit under full-oracle@3. The old covering-set scheduler and its audit
are removed; this issue does not ask to rebuild their estimate or breaker.

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

The historical request was to explain why narrowing did not engage and to
separate reuse refusals from instability. Only the latter remains a current
producer obligation here: a volatile input or unavailable proof is not a
measured execution flip. Unknown/failed attribution must remain unknown/failed.

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

Related: the deleted own-face-gate-suite-decomposition doc (`git log --all --
docs/issues/own-face-gate-suite-decomposition.md`; resolved 2026-09-29 by
the train doctrine — the gate is the probes) (narrowing engaged but
bought little where covering ≈ suite — a different cause with the
same symptom, which is why the ground must be named).

Required resolution: preserve complete deciding oracles and name actual flip
evidence independently of reuse and placement. Advisory attribution must not
block other targets' commits. Historical narrowed counts above stay historical.

Secondary constituent: tooling-redesign 6.2 may admit negative coverage only
with complete harness/locality and candidate-influence authority. Locality alone
does not establish compile-time non-influence.

Lands: tooling-redesign 6.3.
