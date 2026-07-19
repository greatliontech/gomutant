# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [repo-local-cache-split](repo-local-cache-split.md) | Split clean repo cache from dirty or runtime-unverifiable local mutation cache | Clean reusable mutation evidence is written to a repo cache and local-only evidence is written to a local overlay cache |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [runtime-unverifiable-diagnostics](runtime-unverifiable-diagnostics.md) | Name the unstable runtime input, oracle test, and target in unverifiable findings | Unverifiable runtime evidence names the moving input, the oracle test, and the target whose reuse was refused |
| [mcp-progress-parity](mcp-progress-parity.md) | MCP mutation runs lack CLI-equivalent progress and timeout guidance | MCP runs expose progress, partial completion state, and timeout guidance equivalent to the CLI stream |
| [oracle-instability-guidance](oracle-instability-guidance.md) | Package-derived oracles give no guidance when one test makes findings unverifiable | Gomutant identifies unstable oracle tests and suggests or emits an explicit target oracle for stable reuse |
| [findings-committability-check](findings-committability-check.md) | No direct check/export tells whether findings are safe for repo cache | Gomutant reports whether a findings artifact is committable and can export a portable-only version |
| [ephemeral-compile-diagnostics](ephemeral-compile-diagnostics.md) | Ephemeral compile failures hide the underlying Go compiler diagnostic | Ephemeral/manual-mutant compile failures include the underlying compiler error and edit context |
| [gomutant-cache-residue](gomutant-cache-residue.md) | Gomutant cache artifacts appear as changed-scope residue | Changed-scope discovery separates or ignores gomutant-owned cache/config artifacts that cannot produce mutation targets |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [canonical-hash-literal-collapse](canonical-hash-literal-collapse.md) | Canonical body hash collapses literal-interior whitespace, so changed scope omits literal-content edits | The canonical text projection preserves literal contents or changed scope distinguishes literal changes from formatting churn |
| [mutant-runtime-interference](mutant-runtime-interference.md) | Concurrent mutant oracle processes share runtime resources; a collision-induced test failure scores as a kill | Mutant execution isolates shared runtime resources or distinguishes interference failures from mutant-caused kills |
| [ephemeral-input-validation](ephemeral-input-validation.md) | Ephemeral runs score unexercised replacements as survivors and pass unvalidated test packages into go test option positions | Ephemeral runs refuse replacements outside the selected build and test packages that are not loaded import paths |
| [spec-corpus-compile-diagnostics](spec-corpus-compile-diagnostics.md) | `stipulator compile` over docs/specs emits seven diagnostics; coverage gating cannot run clean | `stipulator compile` reports zero diagnostics under the gating Stipulator version |
| [edit-uniqueness-overlap](edit-uniqueness-overlap.md) | Exact-match edit uniqueness counts non-overlapping occurrences, so a self-overlapping pattern applies at a guessed location | Edit uniqueness counts overlapping match starts and refuses more than one as ambiguous |
