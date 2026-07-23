# Compile-rejected candidates re-execute on every serve

Lands: when a serve skips re-executing candidate-local evidence whose reason
is compile rejection while the toolchain and build-configuration pins match,
or the user accepts the permanent splice tax.

## Observed (warm-path measurement)

A finding with any compile-rejected candidate never full-serves: the
rejection contributes candidate-local incomplete evidence ("mutant test
process did not start because the mutant failed to build"), and
REQ-result-stale re-executes flagged candidates under a passing baseline
probe on every serve. Measured on the fixture: the affected target adds a
baseline probe plus a doomed compile per warm run, forever, though the
rejection is deterministic while the toolchain and build-configuration pins
hold - the same pins the serve just validated.

## Resolution

Treat compile-rejection candidate evidence as covered when toolchain and
build-configuration pins match (the rejection cannot change under them), or
persist the rejection as a completed disposition rather than incomplete
evidence. Either ends the permanent splice; the second is the cleaner shape.

Re-measured at gofresh v0.32.0 (post-memo): unchanged - the affected
target still serves with `1 candidate(s) re-execute` and pays the probe
plus doomed compile every warm run. The memo covers observability proofs,
not candidate evidence dispositions.
