# REQ-mcp-liveness's cancellation clause lacks a witness

The requirement's keepalive CONFIGURATION is pinned
(TestServerOptionsCarryKeepalive, bound), but its load-bearing clause —
a failed keepalive ping write cancels every in-flight request context,
aborting the campaign under REQ-exec-cancellation's terms — has no test:
nothing in the named configuration pin exercises the SDK transport seam's
ping-failure path. That specific cancellation path still needs its own witness;
the existence of a whole-requirement binding does not establish it.

Fix direction: a witness over the MCP SDK's session seam — a transport
whose write fails after campaign start, asserting the in-flight run
context cancels within the ping interval (the lifecycle_test.go harness
already builds in-process client/server pairs; a failing-writer
transport wrapper is the missing piece).

The same seam owes the exit line a fact the protocol layer discards: on
a cancellation that wins the protocol layer's race against the
session's end, the session's own error (a wire failure under a signal)
is received into nothing inside the SDK, so the exit line carries only
the context's cause — a connection wrapper remembering the last failed
read or write (the shape the exit-log tests' torn connection already
has) would carry it as `serve-error` on that outcome too.

The same seam owes a second witness, the idle case: a long-lived
session with minutes between calls — sixty-odd calls over one host
session were served before the tools vanished between two turns, with
no probe running — must stay served or fail with a stated reason (the
exit log names every end's class and cause once the session reached
the serve loop; the idle liveness itself is unwitnessed).

Reconcile these transport facts with bounded run recovery and post-write
responses. A live heartbeat does not prove forward progress, and a disconnected
caller must not lose the account of already-persisted work. Use actual transport
fault injection; the historical idle-session observation is not a reproduced
current SDK fault.

Lands: tooling-redesign 6.7.
