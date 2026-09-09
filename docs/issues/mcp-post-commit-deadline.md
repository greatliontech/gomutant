# MCP Deadline After Final Commit

`REQ-exec-cancellation` in `docs/specs/execution.md` makes the final atomic findings
replacement the success boundary. A deadline after that boundary must not turn
the committed result into a failed command. The changed-ref MCP rendering path
can still do so.

This is a source-reachable correctness finding, independently audited at
`0f955e316eb83090b2d24f030844aecd9fcd41d1`. The relevant path is unchanged from
installed consumer revision `01f733a7cb4f26403344469ae360f0e472239255`. It is not
claimed as the cause of the long Stash campaign's missing final response.

## Reachable Path

1. `toolRun` selects a changed-ref cut (`internal/mcpserver/server.go:933-945`).
2. Its final `s.updateStore` returns successfully (`server.go:1236-1265`).
3. The command deadline expires before final survivor-cut rendering.
4. `capRunFindings` calls the cut callback for a non-skipped finding. The callback
   still uses the deadline-bearing context (`server.go:477-490,1280-1283`).
5. With open survivors, nonempty `cut.Added`, and `cut.Changed[f.Symbol]`,
   `CutSurvivorsContext` reaches `BodyHashContext`; cancellation propagates from
   `internal/engine/symbol.go:49-52` through `delta.go:94-100`.
6. `toolRun` returns that error after successful persistence (`server.go:1294-1295`).

Selecting `changed` alone is insufficient to reproduce this path. Empty open
survivors, an empty added-line map, an unchanged symbol, or a skipped finding
prevents the relevant traversal. The survivor need not ultimately lie on an added
line: cancellation happens before position classification.

## Required Regression

Use the store-update seam and a live request transport to expire the tool's own
deadline after a successful final update, before rendering a changed finding with
an open survivor and added lines. Assert persisted results and a successful final
response. Pair it with expiry before final replacement and a failed store update:
neither may claim final success or count an unsuccessful commit.

Keep transport failure distinct from command-deadline expiry. Do not weaken
pre-commit cancellation or fabricate an empty delta to conceal the late error.

Lands: cross-tool train chunk 207
