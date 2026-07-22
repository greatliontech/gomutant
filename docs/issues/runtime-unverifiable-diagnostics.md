# Runtime-unverifiable diagnostics omit the moving input

Lands: when gofresh's View.Validate names the differing source identity (the
moved file) inside ErrViewChanged, so gomutant's drift refusals can carry it.

Landed so far: findings-inspection surfaces name the moved runtime-input
identity, the responsible subject, and the target record (hot-loop chunk 5);
run-side drift refusals are target-local and name the target, the drifted
subject, and gofresh's drift class, and view-construction failures name the
target and oracle (chunk 6). The one remaining gap is closure-class drift
naming the moved FILE: gofresh compares per-file source identities during
validation but reports only the subject and class - the naming needs upstream
attribution in gofresh, not another gomutant wrap.

## Observed

A consuming repository added a PTY-backed test to prove that terminal detection
accepts real terminals and rejects ordinary character devices. A package-derived
mutation run later failed freshness with:

`gofresh: analysis view changed: observation proof for <test>`

The message identified the affected test but not the concrete runtime input that
moved, such as the `/dev/pts/<n>` path or another observed file identity. The
developer had to infer from the test body and recent edits that the PTY
allocation was the unstable input, then create explicit target oracles to keep
that test out of cached package-wide measurements.

The same issue appears with diagnostics like `external directory input: /tmp`:
the findings state explains why reuse is refused at a high level, but not which
observed object or test made the record local-only.

## Resolution

Carry enough observation detail into run and findings inspection output to name
the unstable runtime identity, its producer test process, and the target record
that became stale or unverifiable. The diagnostic should distinguish at least:

- external input path or object identity that moved;
- runtime object created during the test and then reobserved differently;
- observation that was incomplete because the process panicked, timed out, or
  exited before finalization;
- package/test whose oracle pulled the unstable input into the target.

The goal is not to make every unstable observation reusable. The goal is for a
developer to know whether to stabilize the test, narrow the oracle, accept a
local-only cache entry, or improve runtime-input provenance.
