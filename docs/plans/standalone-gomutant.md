# Standalone gomutant

- [x] 1. Contract and resolve atomic multi-file exact-match edit batches against one original snapshot, rejecting invalid or escaping files, missing or ambiguous matches, overlaps, and no-op edits.
- [x] 2. Generalize ephemeral execution to an atomic multi-file Go overlay while preserving baseline probes, attributed outcomes, source-tree immutability, and incomplete-observation evidence.
- [x] 3. Expose multi-file edit batches through MCP for agent dogfooding, retaining whole-replacement and single-file sequential-edit forms and returning every overlaid file consistently.
- [x] 4. Refactor the CLI into `internal/cmd` with one file per command, then add batch input from a JSON file or stdin without compatibility flag rewriting.
- [x] 5. Define findings-document hygiene: whole-tree runs remove entries for symbols no longer present, while changed-scope and explicit-target runs retain every unmeasured entry.
- [x] 6. Make findings inspection evaluate and report current, stale, unverifiable, detached, open, and attested states without serving stale measurements, with deterministic human and machine-readable views.
- [x] 7. Add a CLI target-inspection command equivalent to MCP discovery, rendering the exact symbols, explicit or derived oracles, labels, and changed-but-untargeted residue a subsequent run would use.
- [ ] 8. Record and report the mutation surface by operator and disposition — generated, discarded, killed, and survived — so users can distinguish oracle strength from absent or inapplicable operators.
- [ ] 9. Add standalone run controls for package and symbol filtering, signal-driven cancellation, deterministic progress and final summaries, and explicit cache/force decisions without changing advisory exit semantics.
- [ ] 10. Exercise gomutant against its own packages with bounded repeatable targets, strengthen tests for every non-equivalent survivor found, and record only reasoned equivalent-mutant attestations.
- [ ] 11. Run exhaustive self-hosting and cross-surface verification, resolve document-hygiene residue, update the standalone workflow documentation, and delete this plan before beginning external-producer adapters.
