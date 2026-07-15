# MCP long-running runs

Lands: when native MCP Tasks are supported by the Go SDK, OpenCode, and Claude
Code.

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
cancellation needed for long-running work, but the current Go SDK, OpenCode, and Claude Code do not
all enable that experimental feature. Until they do, agents must use the CLI for work that may
exceed the harness's MCP request timeout. A cancellation observed by gomutant continues to retain
the prior findings document; a private client deadline that is not propagated is not observable by
the server and cannot be treated as cancellation.
