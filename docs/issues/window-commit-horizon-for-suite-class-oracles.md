# A suite-class oracle commits every sixty-four candidates

The execution window's candidate budget is eight candidates per worker
and at least sixty-four, whatever the oracle costs: a six-minute suite
over eight workers commits about every forty-eight minutes, so an
interruption loses up to that much measured work, where a probe-class
oracle commits every few seconds. A budget derived from the bank's
measured durations was built and refused by the spec: REQ-exec-oracle-run
requires audited sets across runs of an unchanged tree to nest, which
holds only while the window partition is a function of the tree and
the target order, never of measured durations (which vary run to run).

A content-stable derivation remains open — the oracle group's test
count or candidate count are properties of the tree and could size
windows so slow suites commit on a horizon without moving the
partition between runs of an unchanged tree. Invariants preserved:
window boundaries a pure function of target order and the budget rule;
the window-scoped flip signal; the audit's nesting across runs. The bank's longest-baseline-per-package
lookup is sound for lifts only (a lift never tightens) and must not
feed a window derivation, which shrinks.

Lands: user decision
