# MCP Cancellation Loses The Banked Summary

The face-neutral `REQ-exec-banked-summary` requires a canceled run that reached
measurement to report its cause, successfully committed findings, kill/open
totals, and selection disposition. MCP's non-drift abort path returns the error
and possible attestation sheds, but not the required banked state. The active
cross-tool train's chunk 207 already charters this face-parity repair; this issue
supplies the source path and acceptance cases for that obligation.

The path was independently audited at `0f955e316eb83090b2d24f030844aecd9fcd41d1`
and is unchanged from installed revision `01f733a7cb4f26403344469ae360f0e472239255`.
No cancellation fixture was executed during this investigation.

## Reachable Path

- Measurement reaches the incremental commit callback (`run.go:3471-3476`).
- `internal/mcpserver/server.go:1178-1206` can persist one target and return nil,
  but does not maintain the successfully-returned commit tally for an abort.
- A subsequent non-drift cancellation returns through `server.go:1221-1225`.
- `shedsRidingAbort` returns the underlying error unchanged when there are no
  sheds; otherwise it appends sheds, not banked totals (`server.go:1374-1378`).
- The pinned MCP SDK v1.6.1 discards typed handler output on an error before
  structured-output serialization (`mcp/server.go:340-353`). Populating an
  otherwise discarded output value would not repair this response.

## Required Regression

Keep the transport alive, let the first incremental commit return successfully,
then expire the command deadline during a later window. Assert that the returned
error/result exposes the cause, exact committed count, kills, opens, and remaining
selection disposition. Include machine-local records: absence of repository-file
rows is not absence of committed work.

Pair with cancellation before measurement and an incremental update that fails
after entering its callback. `postMerge` is populated inside the callback, before
the update returns; it is not a successful-commit ledger. Report only writes whose
commit returned successfully, using their actual post-merge dispositions. Preserve
the separate drift-result and attestation-shed reporting contracts.

Lands: cross-tool train chunk 207
