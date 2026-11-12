# Core/Leaf `.stipulator/policy.textproto` Split & Reviewed `bracket_paths` for Subprocess Witnesses

- **Status:** Open
- **Governing contract:** [`docs/specs/execution.md`](../specs/execution.md), `.stipulator/policy.textproto`
- **Lands:** Chunk 1 in [`docs/plans/plan.md`](../plans/plan.md)

## Problem

Full unscoped `stipulator check` evaluation on `gomutant` (`1,074` bindings, `1,069` witness subjects across `17` packages under `plain_witness: true`) runs in `34m48s` cold / `16m57s` warm and leaves `1,033/1,069` witnesses uncacheable due to two `gomutant` policy configuration gaps (alongside upstream `gofresh` `sync.Once` `dynstate`, tracked `t.Chdir` `testlog` elision/restoration/sealing, `fmt.Fscan*` reader narrowing, and `asmverify` fixes):

1. **Missing Reviewed `bracket_paths` in `.stipulator/policy.textproto` (`319` Uncacheable Witnesses)**
   - `.stipulator/policy.textproto` declares no `bracket_paths`, causing post-run observation bracket checks to reject:
     - `155` witnesses in `internal/engine`: `runtime input not covered by observation bracket: /usr/bin/ps` (process-tree inspection)
     - `144` witnesses across `internal/cmd`, `internal/gitref`, `internal/integrationtest`, and `internal/fixtureguard`: `runtime input not covered by observation bracket: /usr/bin/git`
     - `14` witnesses: `runtime input not covered by observation bracket: /usr/bin/sh`
     - `6` witnesses: `runtime input not covered by observation bracket: analysisbudget_test.go` (root-level file read from a subpackage test whose import-closure bracket excludes `.`)

2. **Single 17-Package `plain` Invocation Throttles Heavy Suites to `GOMAXPROCS=2`**
   - Putting all `17` packages into one `packages: "./..."` invocation sets `SpawnBound = min(16, 17) = 16` and `GOMAXPROCS = 32 / 16 = 2` on a 32-thread host.
   - Once the 14 small packages finish, root `github.com/greatliontech/gomutant` (`564` tests) and `internal/cmd` (`136` tests) run for `14m–25m` constrained to `GOMAXPROCS=2` (which child `go` and `gofresh` operations inside those tests also inherit).

## Resolution Scope

1. Partition `.stipulator/policy.textproto` into `plain:core` (`.`, `./internal/cmd`, `./internal/engine`) and `plain:leaf` (remaining 14 packages), preserving `args: "-test.short=false"` and `plain_witness: true` on both invocations so heavy packages execute with wide `GOMAXPROCS`.
2. Add reviewed `bracket_paths` (`/bin/sh`, `/usr/bin/sh`, `/usr/bin/git`, `/usr/bin/ps`, `internal`, `analysisbudget_test.go`, and any other root fixture/source paths read by subpackage integration tests) to `.stipulator/policy.textproto`.
3. Audit `gomutant` tests for any unisolated `XDG_CACHE_HOME` usage (preserving `GOCACHE` via `go env GOCACHE` beforehand where `XDG_CACHE_HOME` is overridden) and verify warm witness cache hit rates after upstream `gofresh` Chunks 1–6 and `stipulator` Chunks 1–3 land, explicitly dispositioning volatile `/proc` & `/sys/fs/cgroup` reads in `internal/resident` (`resident.Readings()` under `assume_pure: false`) alongside expected-uncacheable `golang.org/x/sys/unix` (`7` subjects) and `testing.Setenv` (`5` subjects) tests.
