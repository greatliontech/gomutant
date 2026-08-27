# Full-suite parallel run once moved a shared-fixture observation bracket

Observed once (2026-08-27, full `go test github.com/greatliontech/gomutant/...`
run): TestRunUsesEnvironmentFrozenAtLoad's survivor bucketed
`unstable-oracle` with RuntimeReason "observation bracket moved: lib" —
the bracket over `internal/engine/testdata/fixturemod/lib` reported
movement inside the run→ingest span. The refusal itself is the seal
working as specified; the defect is the interference. Solo re-run and a
full-suite re-run were both green.

Writer unidentified: ephemeral is overlay-only (REQ-exec-ephemeral, the
tree never touched), every in-place test writer traced targets temp
scaffolds, and mcpserver copies the fixture (CopyFS). The suspicion
class is a cross-package parallel window over the shared fixturemod
(root, internal/engine, internal/cmd, internal/mcpserver all use it;
go test runs packages concurrently), but no concrete write site was
found in one pass.

Ask: identify the writer (instrument the bracket's refusal with WHAT
moved — file list, not just the package — or reproduce under a watcher),
then isolate: mutable-fixture users copy to TempDir, or the fixture
becomes provably read-only for every package. A recurring flake of the
suite gate erodes exactly the evidence the gates exist to produce.

Lands: cross-tool train chunk 113 (gomutant ergonomics and robustness
batch).
