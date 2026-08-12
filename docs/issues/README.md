# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [decision-build-locality](decision-build-locality.md) | decision-view build and body-hash abort campaign-wide on one target's persistent breakage; interlocked with folding the decision batch and the observed proof union into one pass | cross-tool train chunk 39 |
| [init-functions-as-subjects](init-functions-as-subjects.md) | init bodies (registry wiring - classic silent-fault carrier) are loudly excluded from mutation, not measured | cross-tool train chunk 40 |
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | cross-tool train chunk 38 |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | cross-tool train chunk 38 |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | dead-transport detection landed (keepalive + campaign lock); the remaining half is the MCP Tasks surface for client-deadline polling, retrieval, and explicit cancellation | a go-sdk release carrying MCP Tasks (SEP-2663) plus a consuming client |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | cross-tool train chunk 39 |
| [overlay-bypassed-oracle-verdicts](overlay-bypassed-oracle-verdicts.md) | Disk-reading oracles (tree walks, go list subprocesses) bypass the build overlay and report false survivors; label via the existing filesystem-read observations | cross-tool train chunk 39 |
| [env-input-oracle-policy](env-input-oracle-policy.md) | Getwd-to-go.mod repo-root idiom classes as unstable env input; settled direction: per-repo reviewed exemption record (classification refinement rejected - unsound failure direction) | cross-tool train chunk 38 |
| [post-completion-cpu-tail](post-completion-cpu-tail.md) | After the last target committed, the run burned ~100% of one core for 30+ minutes (futex wait, no children, no writes) until externally killed | cross-tool train chunk 39 |
| [oracle-scratch-namespaces](oracle-scratch-namespaces.md) | oracle tests' in-module scratch records missing-arm noise defeating union-equality across runs; needs a declaration surface like pew's directive | cross-tool train chunk 37 |
| [rapid-oracle-records-never-plainly-serve](rapid-oracle-records-never-plainly-serve.md) | rapid-oracle findings fail the plain serve match past the scalar guard and re-measure every campaign; plain-oracle records serve fine | gofresh startup-effect-precision 0a-0c consumed by a dependency bump |
