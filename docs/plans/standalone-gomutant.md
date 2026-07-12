# Standalone gomutant

- [ ] 1. Contract and parse strict apply-patch-style ephemeral mutants over one or more existing tree files, rejecting add/delete/move operations, path escapes, fuzzy or ambiguous hunks, overlaps, and no-op patches.
- [ ] 2. Generalize ephemeral execution to an atomic multi-file Go overlay while preserving baseline probes, attributed outcomes, source-tree immutability, and incomplete-observation evidence.
- [ ] 3. Expose patch mutations through the library, CLI file-or-stdin input, and MCP, retaining whole-replacement and exact-edit forms and returning every overlaid file consistently across surfaces.
- [ ] 4. Define findings-document hygiene: whole-tree runs remove entries for symbols no longer present, scoped runs retain unmeasured entries, and findings inspection identifies detached entries before the next whole-tree run.
- [ ] 5. Make findings inspection evaluate and report current, stale, unverifiable, detached, open, and attested states without serving stale measurements, with deterministic human and machine-readable views.
- [ ] 6. Add a CLI target-inspection command equivalent to MCP discovery, rendering the exact symbols, explicit or derived oracles, labels, and changed-but-untargeted residue a subsequent run would use.
- [ ] 7. Record and report the mutation surface by operator and disposition — generated, discarded, killed, and survived — so users can distinguish oracle strength from absent or inapplicable operators.
- [ ] 8. Add standalone run controls for package and symbol filtering, signal-driven cancellation, deterministic progress and final summaries, and explicit cache/force decisions without changing advisory exit semantics.
- [ ] 9. Exercise gomutant against its own packages with bounded repeatable targets, strengthen tests for every non-equivalent survivor found, and record only reasoned equivalent-mutant attestations.
- [ ] 10. Run exhaustive self-hosting and cross-surface verification, resolve document-hygiene residue, update the standalone workflow documentation, and delete this plan before beginning external-producer adapters.
