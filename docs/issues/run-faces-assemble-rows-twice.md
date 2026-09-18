# The two run faces assemble the delta cut and the per-row layer twice

After the run's final merge, each face walks the rendered records on
its own: the CLI (internal/cmd/run.go's render loop) and the MCP
server (internal/mcpserver/server.go's capRunFindings and the loop
after it) each cut a changed-ref run's open survivors by the delta,
each consult Store.Layer per row for the machine-local reason, and
each sum the delta's open count. One library projection — a row
carrying its layer, its reason, and its delta split, computed once
per rendered record — would let both faces render the same rows and
make the row-level disagreement class (the kind chunk 244's face
audit found) unrepresentable. The findings faces' twin is
findings-row-projections-one-shape.

Lands: cross-tool train chunk 220 (the named smalls; the findings
faces' twin lands with it).
