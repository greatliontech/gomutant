# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [serve-checks-unbatched](serve-checks-unbatched.md) | Serve-path evidence checks pay one window close per subject; one batched check per view would share it | Serve matching batches per view, or the per-subject cost is accepted |
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [ephemeral-replacement-outside-oracle-closure](ephemeral-replacement-outside-oracle-closure.md) | Compiled files outside the oracle's import closure overlay unexercised and read as survivors | Load gains dependency data, or ephemeral results gain execution buckets |
| [test-package-subject-ambiguity](test-package-subject-ambiguity.md) | Same-named helpers across a directory's internal and external test packages read as one ambiguous subject and refuse measurement | Subject resolution keys on package identity, not (directory, identifier) |
| [env-input-oracle-policy](env-input-oracle-policy.md) | Deterministic module-root discovery (Getwd walked to go.mod) buckets symbols unstable-oracle and keeps records machine-local | Refined stable-under-go-test classification, or a per-repo attested oracle exemption |
| [init-func-targets-abort-discovery](init-func-targets-abort-discovery.md) | A changed func init() aborts delta-wide discovery instead of being measured or loudly skipped | init bodies resolve as subjects, or are excluded per-symbol with a decision line |
| [single-proof-failure-aborts-campaign](single-proof-failure-aborts-campaign.md) | A canceled freshness proof for one target exits the whole campaign from prepare | The one proof retries bounded, or its target commits unverifiable with the reason while the campaign proceeds |
