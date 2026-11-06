# Issues

Parked deferrals for `gomutant`. Each open issue names its governing spec requirements and concrete landing slot.

| issue | summary | Lands |
|---|---|---|
| [schema-single-function-compile-and-overlay-reuse](schema-single-function-compile-and-overlay-reuse.md) | Inline `switch` body cloning in `internal/engine/schema.go` expands large functions to 540k+ lines (17 MiB) inside one Go function (27.5m single-core `cmd/compile` SSA stall), per-`testPkg` `pkg-*` overlay paths recompile the mutated archive per downstream oracle, subprocess-`git` tests trigger `mode=serial-full` confirmation storms, and `PWD = effectivePkgDir` lets empty-path-guard mutants write into the live working tree (`REQ-mut-overlay`). | Next `gomutant` engine pass (alongside or immediately after `gofresh` `docs/plans/plan.md` Chunk B.3) |
