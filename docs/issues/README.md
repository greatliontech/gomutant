# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [mcp-liveness-cancellation-witness](mcp-liveness-cancellation-witness.md) | keepalive config pinned but the ping-failure cancellation propagation has no witness over the SDK transport seam | transport-seam fault injection lands in the mcpserver harness |
| [windows-process-fact-arms-unexecuted](windows-process-fact-arms-unexecuted.md) | windows process facts (killed flag, job kills, timeout attribution) are compile-checked only; two attribution defects survived to review state walks | when a windows runner enters the CI matrix |
| [run-scoped-services-through-options](run-scoped-services-through-options.md) | groupBudget/probeGate/scheduleStore ride exported Options as unexported fields; collapse into one explicitly threaded runServices value | with train chunk 136 |
| [ephemeral-attestation-lifecycle](ephemeral-attestation-lifecycle.md) | attestation rows have no prune/retarget lifecycle; commit+dirty stamp is the only staleness signal | with the next change to prune or retarget record surfaces |
| [semantic-closure-in-the-carry-gate](semantic-closure-in-the-carry-gate.md) | a comment-insensitive closure identity would dominate both poles of the carry gate | train chunk 130 (after 129 ships the gofresh identity) |
| [campaign-baseline-needs-scoped-oracles](campaign-baseline-needs-scoped-oracles.md) | suite-scale oracles: single-target campaigns landed and measured (12h43m worst case); chunk-scale extrapolates to weeks — decomposition vs survivor-oracle narrowing vs idle-window acceptance | train chunk 137 (option (b) ruled) |
| [suite-shared-fixture-bracket-flake](suite-shared-fixture-bracket-flake.md) | one full-suite parallel run moved the shared fixturemod/lib observation bracket (writer unidentified; refusal-attribution instrumentation landed in gofresh) | when the instrumented refusal names the writer (next occurrence under a gofresh bump past v0.92.0) |
| [symbol-grammar-package-set-resolution](symbol-grammar-package-set-resolution.md) | symbolPackage's dotted-path guess has one verdict-bearing edge (dotted sibling dirs grant process-attestability across packages); resolve against the loaded package set | when a dotted non-vN package element enters a consumed corpus, or with the next packageProcessAttestable change |
