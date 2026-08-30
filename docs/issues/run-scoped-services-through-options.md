# Run-scoped services ride the exported Options struct

Consolidation candidate (chunk-113 review): `Options` carries three
unexported run-scoped services — `groupBudget`, `probeGate`,
`scheduleStore` — installed by `Run` after `lockCallbacks` and consumed
deep in the execution machinery. An external caller constructing
`Options` cannot set them, so library-level entry points that bypass
`Run`'s installation silently get nil-service semantics (no derived
budgets, no probe gating, no schedule store) with no compile-time
signal.

Sketch: collapse the three into one `runServices` value threaded
explicitly through the execution call chain (or held on the run's own
context object), leaving `Options` a pure caller surface. The
installation site in `Run` becomes the single constructor; nil-service
semantics become unrepresentable for internal callees.

Lands: with train chunk 136.
