# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | Load gains dependency data, or ephemeral results gain execution buckets |
| [warm-floor-view-construction](warm-floor-view-construction.md) | Warm serving floor is per-record view construction; the observability memo covers only proving | View construction stops paying package-load-scale work per record on unchanged trees, or the floor is accepted |
| [compile-rejection-evidence-reexecutes](compile-rejection-evidence-reexecutes.md) | A compile-rejected candidate splices forever - a probe plus a doomed compile per warm run | Serve skips deterministic rejections under matching pins, or the tax is accepted |
| [stale-reason-second-view-build](stale-reason-second-view-build.md) | Stale-decision attribution builds a second subject view per stale target | Attribution derives from the matcher's failed check or reuses the prebuilt views |
