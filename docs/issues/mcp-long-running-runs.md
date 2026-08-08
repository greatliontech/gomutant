# MCP long-running runs

Lands: cross-tool train chunk 41.
## Observed

An exhaustive `gomutant_run` over eight symbols exceeded the MCP client's approximately
120-second deadline and returned only `MCP error -32001: Request timed out`. The equivalent
`gomutant run` CLI invocation completed successfully when given a longer timeout and emitted
preparation and per-symbol progress.

After the MCP timeout, the findings document had been rewritten with some prior current findings
marked `stale` and two measured symbols marked `unverifiable` with reason `mutant test process
panicked before observation finalization`. The targeted package test passed, and a subsequent
narrow CLI rerun measured the same symbols normally. The client-side timeout did not establish
that cancellation reached the server or that the committed run was partial: a client can abandon
its wait while the server completes and atomically commits a result it can no longer deliver.

## Resolution

MCP Tasks provide the protocol-level operation identity, polling, result retrieval, and
cancellation needed for long-running work. As of the 2026-07-28 MCP specification, tasks
moved out of the experimental core into the io.modelcontextprotocol/tasks extension
(poll-based tasks/get plus tasks/update, SEP-2663); go-sdk v1.7.0 carries the new
specification in beta, and no agent client is known to speak the extension yet. Until a
stable SDK and a consuming client exist, agents must use the CLI for work that may
exceed the harness's MCP request timeout - the run tool's description states this and
defaults timeout_sec to 300. A cancellation observed by gomutant continues to retain
the prior findings document; a private client deadline that is not propagated is not
observable by the server and cannot be treated as cancellation.
