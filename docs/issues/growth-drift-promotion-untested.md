# Growth and drift serves are untested for the dirty-to-clean promotion shape

The spec's serve-provenance sentence names five serve flavors; all five
recompute provenance (growth and drift always did), but only the cached
serve, the candidate re-execution splice, and the budget extension have
tests asserting the promotion shape end-to-end — a dirty-born record
serving at a clean tree, re-stamping, and reaching the repo findings
document. Growth and drift tests pin evidence carrying and outcome
splicing, never `Dirty`/`Commit`/layer placement. Pin both: a
dirty-born record, content committed, then an oracle-growth (added
derived test) and a killer-drift (attributable compartment delta) serve
each asserting the re-stamped provenance and the repo-document row.

Lands: cross-tool train chunk 31.
