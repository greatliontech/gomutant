# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [build-selection-oracles](build-selection-oracles.md) | tag-gated/toolchain'd oracles are invisible end to end; single ambient selection by construction, so no divergence bug - a capability boundary | cross-tool train chunk 88 |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | cross-tool train chunk 89 |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | cross-tool train chunk 89 |
