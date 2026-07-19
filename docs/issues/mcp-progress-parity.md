# MCP run progress lacks CLI parity

Lands: when MCP mutation runs expose progress, partial completion state, and
timeout guidance equivalent to the CLI's preparation and per-target stream.

## Observed

An MCP `gomutant_run` over a broad changed-scope target set exceeded the client
deadline and returned only `MCP error -32001: Request timed out`. The equivalent
CLI run showed loading, target resolution, freshness, mutant generation,
baselines, per-symbol measurement decisions, survivors, and the final summary.

After the MCP harness timeout was increased, narrower MCP runs completed and
returned structured decisions, preparation, findings, and summaries. The
successful result was useful, but the failure mode remained much less actionable
than the CLI: when the client abandons the call, the caller cannot tell from the
tool response whether the server is still measuring, whether any target has
committed, or which package should be narrowed.

## Resolution

Provide MCP-visible progress and timeout behavior that matches the CLI's utility.
Until native MCP task polling is available everywhere, the synchronous tool face
should still surface periodic progress events when the host supports them and
return a clear diagnostic on timeout or cancellation that says whether gomutant
observed cancellation, whether a findings write completed, and what command or
target filter can resume the run.

The CLI remains the fallback for very long work, but MCP callers should not have
to switch to the CLI merely to discover what work was in progress.
