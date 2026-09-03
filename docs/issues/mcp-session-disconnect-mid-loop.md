# The MCP server disconnected mid-session with no call in flight

`Lands: user decision`

Observed 2026-09-03 in bldc's adversarial loop: after some sixty
`ephemeral` calls over one host session, every one served, the MCP
server's tools vanished between two turns — the host reported the
server disconnected, then as failed to connect on a later attempt —
with no probe running and the previous probe having returned a kill
normally. Reproduction is unknown; nothing in the host's notice named
a cause (a keepalive failure, an idle timeout, a crash), and the
server writes no log the operator could read afterwards.

The ask: (1) a diagnosable exit — the server logging why it stopped
serving (and where), so a disconnect is attributable; (2) the
liveness witness `mcp-liveness-cancellation-witness` already asks for,
extended to the idle case — a long-lived session with minutes between
calls must stay served or fail with a stated reason. The command line
carried the loop meanwhile (`ephemeral --batch`), at the cost of the
inline edit form a read-only reviewer relies on.
