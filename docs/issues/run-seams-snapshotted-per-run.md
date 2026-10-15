# The run's seams are read from its goroutines as package state

seams (the root's one test-injection struct) is read unsynchronized
from the run's goroutines, so the package's tests run sequentially — a
parallel test swapping a field mid-Run would race the read and leak
into its siblings, a hazard the struct's doc states rather than
excludes. Snapshotting the struct once at Run's and RunEphemeral's
entry (beside the callback mutex wrap) and reading the snapshot from
every goroutine isolates later execution from a subsequent swap. The snapshot
read itself still needs ownership or synchronization if swaps are concurrent;
a copy alone does not make that entry race unrepresentable. The freshness
observers live on the Tree's inspection path, outside a run, and need their own home in
that design.

There is no demonstrated production failure here; the package's seam-swapping
tests are sequential. Separate read-only observers from process-policy mutation
and give each operation a stable composition, including inspection paths outside
Run. Callback presence must not commission execution or change oracle policy.

Lands: tooling-redesign 7.2.
