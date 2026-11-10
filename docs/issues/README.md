# Issues

Parked deferrals for `gomutant`. Each open issue names its governing spec requirements and concrete landing slot.

| issue | summary | Lands |
|---|---|---|
| [shared-cli-mcp-process-utilities](shared-cli-mcp-process-utilities.md) | Evaluate extracting internalized CLI, MCP, resident-memory, process-supervision, and diagnostic-tracing helpers (`internal/guidance`, `internal/resident`, `internal/toolversion`, `internal/shortgates`, `internal/shapecorpus`, `internal/gotool`, `internal/trace`) into a dedicated shared utility module across `pew`, `stipulator`, and `gomutant` (Option 1B). | Defer until user triggers cross-tool CLI/MCP/process utility extraction |
