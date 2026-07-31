# Oracle enumeration can lag the tree while the same run's evidence is current

Lands: the run's derived oracle enumeration is proven fresh against the tree
at run start — an enumeration-source fingerprint or a direct parse of the
package's `_test.go` files cross-checked against the derived set, with a loud
per-target note when the derived set shrinks relative to the prior finding's
oracle — or the load-staleness mechanism is found and closed at its root.
Precondition for any oracle-set-comparison serve rule: a lagging enumeration
reads as set equality and silently serves what a fresh enumeration would
re-measure.

## Observed

A `gomutant run --force` over one package recorded, for several targets, an
`oracleEvidence` set missing exactly the test functions newly added (new
top-level `func TestXxx(t *testing.T)` decls in an existing external
`_test.go` file) shortly before the run — while the same run demonstrably
used the current tree everywhere else: survivor positions reflected a
production-file edit from the same pre-run edit batch, and assertions added
to existing test functions in that same test file took effect (they killed
previously-surviving mutants). Only the new function declarations were absent
from the derived oracle. A fresh `discover` afterward enumerates all tests
correctly, and re-inspection would report those findings "derived oracle
changed" — enumeration against a truly-fresh load is correct; the run's
snapshot was not fresh. The run was a fresh CLI invocation (own pid),
launched roughly 1–2 minutes after the file writes, with a full green
`go test ./...` in between — the on-disk state was complete and buildable.
Once, Linux, go1.26.5; timing-sensitive.

## Where to look

Oracle enumeration reads the engine tree's `packages.Load` snapshot:
`internal/engine/engine.go:75-105` (`Tests: true`, per module member,
`./...`) → `TestsOfContext` walks `t.pkgs[*].Syntax`
(`internal/engine/enumerate.go:71-105`) → `resolveOracleContext`
(`gomutant.go:440`) → memoized per package in `runPreparation.oracle`
(`run.go:197-221`, benign within-run memo). Subject/oracle evidence is built
separately through gofresh views (`freshness.go:93-198`). The defect is in
whatever lets the engine's load observe stale file content when invoked
immediately after writes — rule in/out: `go list`/`packages.Load` overlay or
caching behavior under rapid successive invocations, module-member
enumeration snapshotting, or an engine constructed earlier than the CLI
entry point assumes.

## Why H-grade

The derived oracle is recorded as evidence and drives kill verdicts; a
lagging enumeration silently caps mutation coverage (survivors reported for
mutants the missing tests kill) with no signal anywhere — no skip, no
decision reason, nothing in the findings.

## Repro sketch (synthetic)

In one process-launch sequence: (1) append two new top-level tests to an
existing external `_test.go` file, edit an existing test, and touch a
production file in the same package; (2) immediately
`gomutant run --force --package <pkg>`; (3) compare the recorded
`oracleEvidence` for any target in that package against `discover`'s
enumeration. Expected identical sets; observed the new test functions
missing while both other edits were reflected.
