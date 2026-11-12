# Stipulator Witness Policy Brackets & Core/Leaf Suite Split Plan

## Chunk 1: Core/Leaf `.stipulator/policy.textproto` Split, Reviewed `bracket_paths` & Test Cache Isolation Audit
- [ ] Split `.stipulator/policy.textproto` (`plain_witness: true`, `-test.short=false`) into `plain:core` (`.`, `./internal/cmd`, `./internal/engine` at wide `GOMAXPROCS`) and `plain:leaf` (remaining 14 fast packages)
- [ ] Declare reviewed `bracket_paths` (`/bin/sh`, `/usr/bin/sh`, `/usr/bin/git`, `/usr/bin/ps`, `internal`, `analysisbudget_test.go`, and any other root fixture/source paths read across subpackage integration tests) in `.stipulator/policy.textproto` so subprocess binary lookups and cross-package reads in `internal/engine`, `internal/cmd`, `internal/gitref`, `internal/integrationtest`, and `internal/fixtureguard` are bracket-covered
- [ ] Audit `gomutant` tests for ambient `XDG_CACHE_HOME` accesses (isolating to `t.TempDir()` while preserving `GOCACHE` via `go env GOCACHE` where needed) and verify warm `stipulator check` witness reuse once upstream `gofresh` and `stipulator` fixes land
