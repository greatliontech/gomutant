# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [runtime-unverifiable-diagnostics](runtime-unverifiable-diagnostics.md) | Closure-class drift refusals cannot name the moved file | gofresh View.Validate names the differing source identity inside ErrViewChanged |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [canonical-hash-literal-collapse](canonical-hash-literal-collapse.md) | Canonical body hash collapses literal-interior whitespace, so changed scope omits literal-content edits | The canonical text projection preserves literal contents or changed scope distinguishes literal changes from formatting churn |
| [mutant-runtime-interference](mutant-runtime-interference.md) | Concurrent mutant oracle processes share runtime resources; a collision-induced test failure scores as a kill | Mutant execution isolates shared runtime resources or distinguishes interference failures from mutant-caused kills |
| [ephemeral-input-validation](ephemeral-input-validation.md) | Ephemeral runs score unexercised replacements as survivors and pass unvalidated test packages into go test option positions | Ephemeral runs refuse replacements outside the selected build and test packages that are not loaded import paths |
| [spec-corpus-compile-diagnostics](spec-corpus-compile-diagnostics.md) | `stipulator compile` over docs/specs emits seven diagnostics; coverage gating cannot run clean | `stipulator compile` reports zero diagnostics under the gating Stipulator version |
| [stale-reason-second-view-build](stale-reason-second-view-build.md) | Stale-decision attribution builds a second subject view per stale target | 11 of the active hot-loop-ux plan, or attribution derives from the matcher/prebuilt views |
