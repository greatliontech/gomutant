# MCP run timeout commits partial findings

Lands: when MCP long-running run semantics or findings-commit cancellation next change.

## Observed

An exhaustive `gomutant_run` over eight symbols exceeded the MCP client's approximately
120-second deadline and returned only `MCP error -32001: Request timed out`. The equivalent
`gomutant run` CLI invocation completed successfully when given a longer timeout and emitted
preparation and per-symbol progress.

After the MCP timeout, the findings document had been rewritten with some prior current findings
marked `stale` and two measured symbols marked `unverifiable` with reason `mutant test process
panicked before observation finalization`. The targeted package test passed, and a subsequent
narrow CLI rerun measured the same symbols normally. A transport deadline can therefore leave a
misleading committed findings state rather than preserving the prior atomic document.

## Resolution

Define a long-running MCP operation that reports progress or can be resumed beyond the client
deadline. Cancellation or transport timeout must not commit partial or synthetic-unverifiable
findings: either complete and atomically replace the requested measurements, or retain the prior
document and report cancellation. The error must distinguish client deadline, server cancellation,
oracle timeout, test panic, and findings-commit failure.
