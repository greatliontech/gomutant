# The two run faces assemble the delta cut and the per-row layer twice

After the run's final merge, each face walks the rendered records on
its own: the CLI (internal/cmd/run.go's render loop) and the MCP
server (internal/mcpserver/server.go's capRunFindings and the loop
after it) each cut a changed-ref run's open survivors by the delta,
each consult the ledger's routed placement for the machine-local reason, and
each sum the delta's open count. One library projection — a row
carrying its layer, its reason, and its delta split, computed once
per rendered record — would let both faces render the same rows and
remove the duplicate row assembly. The findings faces' twin is
findings-row-projections-one-shape.

The current readers use `ledger.Layer`/`Routing.Of`, preserving the write's own
routing decision; do not re-judge placement while projecting. Fold duplicate
assembly under the closing context and keep partial rows, cumulative counts and
the persisted account when later cutting or rendering fails.

Secondary constituent: tooling-redesign 8.3 reconciles the common wire projection
with [findings-row-projections-one-shape.md](findings-row-projections-one-shape.md).

Lands: tooling-redesign 6.7.
