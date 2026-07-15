# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [runtime-input-provenance](runtime-input-provenance.md) | Prove reusable runtime evidence across producer-created outputs | Observation-time object provenance distinguishes produced outputs from external inputs on every supported host |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | A caller needs adequacy evidence for a structural assertion's oracle |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | A caller repeatedly needs manual mutants for generated data, resolver seams, or caller mappings |
| [mcp-run-timeout-commits-partial-findings](mcp-run-timeout-commits-partial-findings.md) | MCP run deadlines fail opaquely and can replace current evidence with partial stale/unverifiable findings | MCP long-running run semantics or findings-commit cancellation next change |
