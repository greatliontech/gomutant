# Split God-Files (`run.go`, `findings.go`, `internal/mcpserver/server.go`), Deduplicate `explain`, and Fix Unbatched Freshness Checks

**Lands:** Chunk 8 in `gofresh/docs/plans/plan.md` (`Downstream Fleet Refactoring, Batching & Startup Purity`).

## Context & Concrete Findings

### 1. Unbatched Per-Row `s.checkContext` & Un-narrowed Module-Wide `reobserveBase` (`freshness.go:896-987, 1060-1137, 1309-1352`, `run.go:1869, 2105, 2180, 3510-3554`, `bank.go:518`)

1. **Per-Row `s.checkContext` and Unmemoized `CurrentRuntimeInputs` in `judgeAdmittedContext` / `inspectContext` (`freshness.go:949-987, 1309-1352`)**:
   - `inspectFindings` (`freshness.go:1060-1137`) and the end-of-run `opts.Posture` loop (`run.go:3510-3554`) construct a module-wide `*subjectViewSet` (`buildSubjectViews`) where each module's `*gofresh.Snapshot` spans all subjects in the module.
   - `judgeAdmittedContext` calls `inspectContext` once for `f.TargetEvidence` (`freshness.go:1324`) and once for every row in `adm.oracle` (`freshness.go:1339`).
   - Inside `inspectContext`:
     - Line 956 calls `gofresh.CurrentRuntimeInputs(ctx, evidence.RuntimeInputs, ...)` without a `runtimeMemo`, re-statting the same union `RuntimeInputs` manifest on every oracle row.
     - Line 975 calls `s.checkContext(ctx, evidence.fingerprint())` (`s.view.CheckObserved` or `s.view.Check`) **one row at a time** on the module-wide `s.view`. Because `deferredCheckClose` is `false` and `evidence.RuntimeInputs != ""`, every single row whose closure matches triggers `v.reobserveBase(ctx)` across the entire module ($1100+$ full-module `observeView` passes for 100 findings with 10 oracle rows).
2. **Un-narrowed Module-Wide `view.CheckObservedBatch` Per Target in `evidencePairsValid` (`freshness.go:896-947`)**:
   - Unlike `forTarget` (`freshness.go:743-772`, which narrows the producer view via `group.module.view.Sibling(group.subjects)`), `evidencePairsValid` calls `view.CheckObservedBatch(ctx, batch.recorded)` (`freshness.go:936`) on the un-narrowed module-wide `view` once per cached target, and checks non-observed fingerprints (`!observedFingerprint(fingerprint)`, lines 911–924) one by one instead of batching via `view.CheckBatch`.

### 2. `freshForContext` Builds `gofresh` `Snapshot` Before Checking Cheap View-Free Pin Preconditions (`findings.go:2558-2624`)

- `freshForContext` calls `t.newSubjectViews(ctx, symbols, ...)` (`findings.go:2576`, constructing a `gofresh.Session` and `Snapshot`) before checking `!budgetCovers(f, budget)` (`findings.go:2585`), `len(f.CandidateEvidence) != 0` (`findings.go:2623`), `f.EvidenceFault != ""`, `f.OperatorSet != engine.OperatorSet`, or `!timeoutPinMatches(...)`.

### 3. Unbatched `LinkedTestPackagesContext` in `OracleClosureSignpostContext` and Eager Whole-Workspace `DerivedOracleContext` (`gomutant.go:680-691`, `internal/engine/buildset.go:424-509`, `internal/engine/engine.go:138-140`)

- `OracleClosureSignpostContext` (`gomutant.go:680-691`) loops over `changed` calling `t.eng.LinkedTestPackagesContext(ctx, pkg)` one package at a time (spawning one `go list -deps -test` per package) instead of batching via `warmLinkedTestPackagesContext`.
- `DerivedOracleContext(ctx, pkgPath)` (`internal/engine/buildset.go:424-509`) parses on-disk test files and runs `go list -test` across every package in the workspace even for single-symbol queries, because `packages.Config.Mode` (`internal/engine/engine.go:138-140`) omits `packages.NeedImports | packages.NeedDeps` even though `NeedTypes` already passes `-deps=true` to `go list`.

### 4. Duplicated `explain` Domain Logic Between CLI and MCP (`internal/cmd/explain.go:100-224`, `internal/mcpserver/server.go:1806-1934`)

- Single-symbol selection resolution (`AttestedLoadSelection`, `InspectFinding`, `LayerReasons`, `RollUpMachineLocalInputs`, survivor advice) and document promotion triage aggregation (`RecordFilter` admission, `repo`/`local` counting, clause roll-up and sorting) are duplicated between `internal/cmd/explain.go` and `internal/mcpserver/server.go`.

### 5. Oversized Multi-Concern Files (`run.go` [3,572 lines], `findings.go` [3,306 lines], `internal/mcpserver/server.go` [2,413 lines])

- `run.go` (`3,572` lines, including a 2,700-line `runCounted` function) mixes option/event types, target/view resolution, target preparation, worker-pool window execution, window aggregation, and ready-window scheduling.
- `findings.go` (`3,306` lines) mixes domain types, v11–v17 JSON schema parsing/validation, `Tree.Fresh`/`FreshFor`, multi-selection merge/grafting, and file lock/update + `AttestFinding`.
- `internal/mcpserver/server.go` (`2,413` lines) puts all 8 MCP tool handlers, rotating file logging, and heartbeat plumbing in a single file.

## Resolution

During Chunk 8:
1. Split `findings.go` (`findings_parse.go`, `findings_merge.go`), `run.go` (`run_prepare.go`, `run_window.go`, `run_aggregate.go`, and decompose `runCounted` into phase methods), and `internal/mcpserver/server.go` (per-tool files matching `internal/cmd/`) within their packages so all `.stipulator/bindings/` symbols remain intact.
2. Deduplicate `explain` domain logic into root `package gomutant` (`ExplainSymbol` / `ExplainTriage`), shared by `internal/cmd/explain.go` and `internal/mcpserver`.
3. Share `runtimeMemo` and batch/deduplicate `CheckObservedBatch` / `CheckBatch` checks in `inspectFindings`, `opts.Posture`, and `evidencePairsValid` (with narrowed `Sibling` views per target package).
4. Reorder `freshForContext` to evaluate view-free pin predicates before `t.newSubjectViews`, and batch `LinkedTestPackagesContext` in `OracleClosureSignpostContext` + include `NeedImports | NeedDeps` in `engine.go`.
