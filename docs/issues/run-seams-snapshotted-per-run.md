# The run's seams are read from its goroutines as package state

seams (the root's one test-injection struct) is read unsynchronized
from the run's goroutines, so the package's tests run sequentially — a
parallel test swapping a field mid-Run would race the read and leak
into its siblings, a hazard the struct's doc states rather than
excludes. Snapshotting the struct once at Run's and RunEphemeral's
entry (beside the callback mutex wrap) and reading the snapshot from
every goroutine makes the race unrepresentable: a swap after entry
cannot reach a running run. The three freshness observers live on the
Tree's inspection path, outside a run, and need their own home in
that design.

Lands: cross-tool train chunk 245 (the run's decomposition; slotted at audit 262 — a derivable design is slotted, not parked) (a production structure change with no failing
shape today — the package runs no parallel test).
