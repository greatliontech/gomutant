# Issues

Parked deferrals for `gomutant`. Each open issue names its governing spec requirements and concrete landing slot.

| issue | summary | Lands |
|---|---|---|
| [stipulator-policy-brackets-and-suite-isolation](stipulator-policy-brackets-and-suite-isolation.md) | `.stipulator/policy.textproto` bundles all 17 packages into one `plain` invocation (`GOMAXPROCS=2` on heavy root/`internal/cmd`/`internal/engine` suites) and omits reviewed `bracket_paths` (`/usr/bin/ps`, `/usr/bin/git`, `/usr/bin/sh`), leaving `319` subprocess/cross-package witnesses uncacheable alongside upstream `gofresh` `testlog`/`dynstate` gaps. | Chunk 1 in [`docs/plans/plan.md`](../plans/plan.md) |
| [shared-cli-mcp-process-utilities](shared-cli-mcp-process-utilities.md) | Evaluate extracting internalized CLI, MCP, resident-memory, process-supervision, and diagnostic-tracing helpers (`internal/guidance`, `internal/resident`, `internal/toolversion`, `internal/shortgates`, `internal/shapecorpus`, `internal/gotool`, `internal/trace`) into a dedicated shared utility module across `pew`, `stipulator`, and `gomutant` (Option 1B). | Defer until user triggers cross-tool CLI/MCP/process utility extraction |
