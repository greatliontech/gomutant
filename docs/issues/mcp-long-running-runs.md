# MCP long-running runs

Lands: a go-sdk release carrying the MCP Tasks extension (SEP-2663) plus a consuming agent client - task-based polling, result retrieval, and explicit cancellation for runs beyond a request deadline.
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

## Landed half

Dead-transport detection landed independently of MCP Tasks: the server
keepalive-pings its session, a failed ping write cancels every in-flight
request context, and the campaign aborts under REQ-exec-cancellation
(mcp.md REQ-mcp-lifecycle). The campaign lock (REQ-exec-exclusivity)
keeps any surviving detached campaign from interleaving with a retry.
What remains for this issue is the protocol-level task surface above:
polling, result retrieval after a client deadline, and explicit
cancellation of a run the client abandoned while its connection lives.
