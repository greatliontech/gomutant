# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [build-selection-oracles](build-selection-oracles.md) | tag-gated/toolchain'd oracles are invisible end to end; single ambient selection by construction, so no divergence bug - a capability boundary | a campaign needs a tag-gated or toolchain'd oracle |
| [skip-worktree-false-clean](skip-worktree-false-clean.md) | skip-worktree/assume-unchanged entries stamp provenance clean while the measurement read divergent bytes | a field report shows a measured tree using either flag |
| [multi-package-testlog-clobber](multi-package-testlog-clobber.md) | a multi-package observed engine run clobbers its one testlog; unreachable from Tree.Run (single-package groups), exported-API hazard only | when an engine consumer passes multiple test packages to an observed mutant run |
| [decision-build-locality](decision-build-locality.md) | decision-view build and body-hash abort campaign-wide on one target's persistent breakage; interlocked with folding the decision batch and the observed proof union into one pass | cross-tool train chunk 39 |
| [init-functions-as-subjects](init-functions-as-subjects.md) | init bodies (registry wiring - classic silent-fault carrier) are loudly excluded from mutation, not measured | cross-tool train chunk 40 |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | dead-transport detection landed (keepalive + campaign lock); the remaining half is the MCP Tasks surface for client-deadline polling, retrieval, and explicit cancellation | a go-sdk release carrying MCP Tasks (SEP-2663) plus a consuming client |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | cross-tool train chunk 39 |
| [overlay-bypassed-oracle-verdicts](overlay-bypassed-oracle-verdicts.md) | Disk-reading oracles (tree walks, go list subprocesses) bypass the build overlay and report false survivors; label via the existing filesystem-read observations | cross-tool train chunk 39 |
| [post-completion-cpu-tail](post-completion-cpu-tail.md) | After the last target committed, the run burned ~100% of one core for 30+ minutes (futex wait, no children, no writes) until externally killed | cross-tool train chunk 39 |
