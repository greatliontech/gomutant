# Full-suite parallel run once moved a shared-fixture observation bracket

Observed once (2026-08-27, full `go test github.com/greatliontech/gomutant/...`
run): TestRunUsesEnvironmentFrozenAtLoad's survivor bucketed
`unstable-oracle` with RuntimeReason "observation bracket moved: lib" —
the bracket over `internal/engine/testdata/fixturemod/lib` reported
movement inside the run→ingest span. The refusal itself is the seal
working as specified; the defect is the interference. Solo re-run and a
full-suite re-run were both green. Not reproduced since (through
2026-08-31's suite runs).

Writer unidentified after one static pass (ephemeral is overlay-only,
in-place writers target temp scaffolds, mcpserver copies the fixture);
the suspicion class is a cross-package parallel window over the shared
fixturemod. The chartered instrumentation LANDED: gofresh's
moved-bracket refusal now names WHAT moved — members added/removed by
name, or the most recently touched members with mtimes
(runtimeinput.bracketMoveAttribution, gofresh > v0.92.0) — so the next
occurrence carries its lead. gomutant consumes it at its next gofresh
bump.

Remaining ask: on the next occurrence, read the named file/mtime,
identify the writer, then isolate (mutable-fixture users copy to
TempDir, or the fixture becomes provably read-only per package).

Lands: when the instrumented refusal names the writer (next
occurrence under a gofresh bump past v0.92.0).
