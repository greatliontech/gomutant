# The findings faces project InspectedRecord three ways

The one inspection walk (`gomutant.InspectDocument`) answers rows as
data (`InspectedRecord`); the CLI projects them into `findingView`
(with a `*[]Survivor` delta whose presence says the cut ran, and an
unexported split for the human `[delta]` marks) and the MCP into
`findingSummary` (a count) and `inspectedFinding` (a `[]Survivor`
delta) — three shapes of one row, differing in the delta's
representation alone. One wire shape carrying the pointer semantics,
with the summary as its projection, would delete both face loops.

Lands: cross-tool train chunk 220
