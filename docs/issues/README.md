# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [mcp-liveness-cancellation-witness](mcp-liveness-cancellation-witness.md) | keepalive config pinned but the ping-failure cancellation propagation has no witness over the SDK transport seam | transport-seam fault injection lands in the mcpserver harness |
| [windows-process-fact-arms-unexecuted](windows-process-fact-arms-unexecuted.md) | windows process facts (killed flag, job kills, timeout attribution) are compile-checked only; two attribution defects survived to review state walks | when a windows runner enters the CI matrix |
| [ephemeral-attestation-lifecycle](ephemeral-attestation-lifecycle.md) | attestation rows have no prune/retarget lifecycle; commit+dirty stamp is the only staleness signal | with the next change to prune or retarget record surfaces |
| [planonly-gatherwindow-suite-hang](planonly-gatherwindow-suite-hang.md) | a plan-only Tree.Run hangs the suite at gatherWindow, cause undumped | train chunk 113 (closes unreproducible at triage absent a mechanism-showing dump) |
| [derive-probe-oracle-timeout](derive-probe-oracle-timeout.md) | derive the mutant-run budget from the measured baseline instead of a fixed timeout knob | train chunk 113 |
| [symbol-cutter-duality](symbol-cutter-duality.md) | two symbol cutters over one grammar, relationship unstated | train chunk 113 |
| [semantic-closure-in-the-carry-gate](semantic-closure-in-the-carry-gate.md) | a comment-insensitive closure identity would dominate both poles of the carry gate | train chunk 130 (after 129 ships the gofresh identity) |
| [campaign-baseline-needs-scoped-oracles](campaign-baseline-needs-scoped-oracles.md) | root-suite scale blocks campaign baselines in gomutant (~16m) AND gofresh (~6.5m x 303 candidates, measured 2h21m/0 committed); chunk gates stand on ephemeral probes; go-test face resolved by CI's measured -timeout | train chunk 113 |
| [graceful-interrupt-persistence](graceful-interrupt-persistence.md) | SIGINT finishes the mutant and persists partial-target resume state machine-locally | train chunk 113 |
| [method-rewrite-unit-oracle](method-rewrite-unit-oracle.md) | engine-scoped campaign oracle misses methodDeclarationRewrite's root-package teeth — 86 machine-local survivors at 0 kills; line-235 sample measured 3-3 artifact/gap, decoy fixture landed; scoping fix + drift-pin/offset-bounds arms + stable-oracle re-run | train chunk 113 |
- **[suite-shared-fixture-bracket-flake](suite-shared-fixture-bracket-flake.md)** — one full-suite
  parallel run moved the shared fixturemod/lib observation bracket (honest refusal, unidentified
  writer; solo and full re-runs green); identify the writer, then isolate mutable-fixture users.
  *Lands: cross-tool train chunk 113.*
