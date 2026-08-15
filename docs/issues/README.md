# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the
load-bearing rationale is promoted inline to the spec / a test, and the doc
is deleted (git holds history).

| slug | summary | Lands |
|------|---------|-------|
| [build-selection-oracles](build-selection-oracles.md) | tag-gated/toolchain'd oracles are invisible end to end; single ambient selection by construction, so no divergence bug - a capability boundary | cross-tool train chunk 88 |
| [concurrent-campaigns-stomp-process-width](concurrent-campaigns-stomp-process-width.md) | a second campaign in one long-lived server installing a narrower width can split a first campaign's later oracle envs from its evidence env; fail-safe (degrade/re-measure, never stale) | cross-tool train chunk 41 |
| [init-functions-as-subjects](init-functions-as-subjects.md) | init bodies (registry wiring - classic silent-fault carrier) are loudly excluded from mutation, not measured | cross-tool train chunk 40 |
| [structural-mutation-class](structural-mutation-class.md) | Structural mutants (forbidden import, broken method set) so analyzer-shaped oracles get a teeth check | cross-tool train chunk 89 |
| [integration-mutation-recipes](integration-mutation-recipes.md) | Recipe-shaped mutation classes for generator drift, parser guards, resolver and caller seams | cross-tool train chunk 89 |
| [mcp-long-running-runs](mcp-long-running-runs.md) | dead-transport detection landed (keepalive + campaign lock); the remaining half is the MCP Tasks surface for client-deadline polling, retrieval, and explicit cancellation | cross-tool train chunk 41 |
| [mcp-server-refuses-newer-cli-document](mcp-server-refuses-newer-cli-document.md) | a long-running MCP server older than the CLI refuses the v7 document with a bare version error; surface dead until restart, cause invisible | cross-tool train chunk 41 |
| [killed-mutant-oracle-scratch-residue](killed-mutant-oracle-scratch-residue.md) | killed mutants skip test cleanup; scratch residue with mutant-mangled modes breaks later clean runs - consumer-hygiene guidance wanted | cross-tool train chunk 41 |
