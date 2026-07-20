# Mid-run freshness drift aborts opaquely, possibly with exit 0

Lands: 6 of the active hot-loop-ux plan (the gofresh seam context chunk).

## Observed

A run racing a concurrent tree edit died partway with
`validate freshness: analysis view changed: closure for <symbol>` — the
invariant it protects (measurements bind to one tree state) is sound, but
the landing is not:

- The message names neither the moved file nor the recovery, and an agent
  reported the process appeared to exit 0, which lets a pipeline read a
  half-measured campaign as success. The exit-code claim is unverified —
  verify it first; a zero exit on an aborted campaign is a defect, not UX.
- No continuation: targets whose closures did not move are discarded with
  the aborted run.

## Resolution

Verify and fix the exit code if the report holds. Name the moved input
(the per-input attribution the gofresh bump provides) and the recovery in
the abort message; either continue with still-stable targets or end with
an explicit "tree changed under measurement; re-run" — the invariant
becomes legible instead of surprising.

## Live reproduction (2026-07-20, gofresh corpus)

`gomutant run --changed HEAD~2` over gofresh/runtimeinput: 33 minutes,
639 candidates measured across ten symbols, then the terminal line
`gomutant: validate freshness: gofresh: analysis view changed:
observation proof for <package>.TestAbsoluteIdentityCoverageRequiresAbsoluteRoot`
- no findings written, the whole campaign discarded. The likely mover
was a concurrent gomutant campaign on another repository churning the
shared GOCACHE: exactly the drift class current gofresh's
guard-covered build-cache root eliminates, unreachable until this
project's bump chunk lands and passes the new roots. The message names
the test whose proof moved but neither the moved input nor a next
action (re-run? scope down? what moved?), and the exit code of the
observed run was masked by the caller's pipeline - the exit-0 suspicion
above remains to be verified (a re-run with the code captured is in
flight).
