# The campaign's baseline probe seam returns a six-value tuple

`engine.TestProbeObservedEnv` — and the two seam variables the run
and the schedule swap in tests — returns (ran, passed, failed,
diagnostic, state, err); the engine already holds the same facts in
one `probeResult`. Callers discard different members, and each new
fact widens every caller and seam wrapper. The collapse: export the
result (Ran, Passed, Failed, Diagnostic, State) and return it with
the error; the seam wrappers and the nine engine test call sites
follow.

Lands: cross-tool train chunk 214
