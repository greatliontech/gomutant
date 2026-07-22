# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [staged-snapshot-run-mode](staged-snapshot-run-mode.md) | Measure staged/index snapshots as clean content before commit | Gomutant can run against the staged index or another explicit content snapshot and produce clean evidence for it |
| [runtime-unverifiable-diagnostics](runtime-unverifiable-diagnostics.md) | Closure-class drift refusals cannot name the moved file | gofresh View.Validate names the differing source identity inside ErrViewChanged |
| [mcp-progress-parity](mcp-progress-parity.md) | MCP mutation runs lack CLI-equivalent progress and timeout guidance | MCP runs expose progress, partial completion state, and timeout guidance equivalent to the CLI stream |
| [oracle-instability-guidance](oracle-instability-guidance.md) | Package-derived oracles give no guidance when one test makes findings unverifiable | Gomutant identifies unstable oracle tests and suggests or emits an explicit target oracle for stable reuse |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-long-running-runs](mcp-long-running-runs.md) | MCP client abandonment is not observable when cancellation is not propagated | Native MCP Tasks are supported by the Go SDK, OpenCode, and Claude Code |
| [canonical-hash-literal-collapse](canonical-hash-literal-collapse.md) | Canonical body hash collapses literal-interior whitespace, so changed scope omits literal-content edits | The canonical text projection preserves literal contents or changed scope distinguishes literal changes from formatting churn |
| [mutant-runtime-interference](mutant-runtime-interference.md) | Concurrent mutant oracle processes share runtime resources; a collision-induced test failure scores as a kill | Mutant execution isolates shared runtime resources or distinguishes interference failures from mutant-caused kills |
| [ephemeral-input-validation](ephemeral-input-validation.md) | Ephemeral runs score unexercised replacements as survivors and pass unvalidated test packages into go test option positions | Ephemeral runs refuse replacements outside the selected build and test packages that are not loaded import paths |
| [spec-corpus-compile-diagnostics](spec-corpus-compile-diagnostics.md) | `stipulator compile` over docs/specs emits seven diagnostics; coverage gating cannot run clean | `stipulator compile` reports zero diagnostics under the gating Stipulator version |
| [edit-uniqueness-overlap](edit-uniqueness-overlap.md) | Exact-match edit uniqueness counts non-overlapping occurrences, so a self-overlapping pattern applies at a guessed location | Edit uniqueness counts overlapping match starts and refuses more than one as ambiguous |
| [stale-reason-second-view-build](stale-reason-second-view-build.md) | Stale-decision attribution builds a second subject view per stale target | 11 of the active hot-loop-ux plan, or attribution derives from the matcher/prebuilt views |
| [targets-flag-help-says-document-means-path](targets-flag-help-says-document-means-path.md) | CLI --targets help promises inline JSON but the value is a path | The help matches the semantics or both forms are accepted |
| [targets-fed-run-output-ux](targets-fed-run-output-ux.md) | Deduplicate skip lines; hint the next step on type-symbol skips | Targets-fed runs report skip classes once, with a methodology hint for type symbols |
| [goroutine-panic-kill-aborts-run](goroutine-panic-kill-aborts-run.md) | Classify baseline-clean goroutine-panic crashes as kills; stop aborting the run (and discarding measured symbols) over one unclassifiable mutant | A mutant whose failure reproduces under the mutant and clears the baseline probe is a kill regardless of test attribution, and unclassifiable mutants record as unverifiable without aborting the campaign |
- **[discover-run-changed-disagree](discover-run-changed-disagree.md)** — same tree, same
  --changed flag: discover listed seven symbols, run measured ten; the omissions were the
  newly added functions and the edited host function carrying most candidates. discover's
  output also buries the counts. *Lands: 10 of the active hot-loop-ux plan, or earlier if
  targeting is touched.*
