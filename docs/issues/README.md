# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [decision-build-locality](decision-build-locality.md) | decision-view build and body-hash abort campaign-wide on one target's persistent breakage; interlocked with folding the decision batch and the observed proof union into one pass | user decision |
| [init-functions-as-subjects](init-functions-as-subjects.md) | init bodies (registry wiring - classic silent-fault carrier) are loudly excluded from mutation, not measured | init bodies become measurable subjects end-to-end, or the exclusion is settled permanent in the targeting spec |
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | Load gains dependency data, or ephemeral results gain execution buckets |
| [env-input-oracle-policy](env-input-oracle-policy.md) | Getwd-to-go.mod repo-root idiom classes as unstable env input; settled direction: per-repo reviewed exemption record (classification refinement rejected - unsound failure direction) | A consuming repository needs committable witness evidence under a deliberately kept environment-reading idiom |
| [post-completion-cpu-tail](post-completion-cpu-tail.md) | After the last target committed, the run burned ~100% of one core for 30+ minutes (futex wait, no children, no writes) until externally killed | The tail is profiled via SIGQUIT dump and the spin closed, or shown benign and bounded |
| [oracle-scratch-namespaces](oracle-scratch-namespaces.md) | oracle tests' in-module scratch records missing-arm noise defeating union-equality across runs; needs a declaration surface like pew's directive | with the runtimeinput producer facade |
| [fresh-stamp-manifest-base](fresh-stamp-manifest-base.md) | fresh-measure dirtiness resolves observation manifests against the git toplevel and Committable against the one store module dir - in monorepo/workspace layouts runtime-input dirt is invisible and a false-clean portable row can land (re-measure cost, never a wrong verdict) | with the first monorepo/workspace corpus consuming committable findings, or when provenance path resolution next changes |
| [growth-drift-promotion-untested](growth-drift-promotion-untested.md) | growth and drift serves recompute provenance but no test pins their dirty-to-clean promotion shape (Dirty/Commit/layer placement) | when the growth or drift serve next changes, or with the first field report of a growth/drift serve failing to promote |
