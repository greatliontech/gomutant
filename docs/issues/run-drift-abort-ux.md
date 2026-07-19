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
