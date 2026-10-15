# Some sixty hand-written seam save/restore pairs, seven restoring to a literal

The original survey found about forty-five seam save/restore pairs across
twelve root test files
and some fifteen more across the two faces' test files after the seams
became one struct per package; seven restored to a literal (the default)
rather than the prior value, correct only because the default was what
stood. One helper — swap(t, &seams.field, value) with a t.Cleanup
restoring the prior — centralizes restoration and deletes
the boilerplate the struct made uniform.

The current seam structs still require callers to manage swaps and cleanup.
Re-census their live uses after migration; the counts above are the filing's
inventory, not new measurements. A helper should restore the actual prior value
and preserve the tests' sequential ownership; it cannot by itself make concurrent
global mutation safe. Keep instrumentation separate from execution-policy seams.

Secondary constituent: tooling-redesign 7.2 owns the per-operation seam boundary.

Lands: tooling-redesign 5.6.
