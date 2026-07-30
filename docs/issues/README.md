# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [init-functions-as-subjects](init-functions-as-subjects.md) | init bodies (registry wiring - classic silent-fault carrier) are loudly excluded from mutation, not measured | init bodies become measurable subjects end-to-end, or the exclusion is settled permanent in the targeting spec |
| [surface-read-failure-mislabels-decl-deleted](surface-read-failure-mislabels-decl-deleted.md) | A body-source read failure mid-scan silently classes the declaration as deleted | Unreadable working-side declarations report as unreadable or fail the scan |
| [serve-checks-unbatched](serve-checks-unbatched.md) | Serve-path evidence checks pay one window close per subject; one batched check per view would share it | Serve matching batches per view, or the per-subject cost is accepted |
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | Load gains dependency data, or ephemeral results gain execution buckets |
| [env-input-oracle-policy](env-input-oracle-policy.md) | Getwd-to-go.mod repo-root idiom classes as unstable env input; settled direction: per-repo reviewed exemption record (classification refinement rejected - unsound failure direction) | A consuming repository needs committable witness evidence under a deliberately kept environment-reading idiom |
