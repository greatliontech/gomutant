# REQ-mcp-liveness's cancellation clause lacks a witness

The requirement's keepalive CONFIGURATION is pinned
(TestServerOptionsCarryKeepalive, bound), but its load-bearing clause —
a failed keepalive ping write cancels every in-flight request context,
aborting the campaign under REQ-exec-cancellation's terms — has no test:
nothing exercises the SDK transport seam's ping-failure path. A binding
cannot express a per-clause shortfall, so the gap rides here.

Fix direction: a witness over the MCP SDK's session seam — a transport
whose write fails after campaign start, asserting the in-flight run
context cancels within the ping interval (the lifecycle_test.go harness
already builds in-process client/server pairs; a failing-writer
transport wrapper is the missing piece).

Lands: when a transport-seam fault injection lands in the mcpserver
test harness (the lifecycle_test in-process pair growing a
failing-writer arm).
