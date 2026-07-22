# Plan: hot-loop UX — current evidence stack, attributable verdicts, agents are first-class

Spec: `docs/specs/overview.md`, `docs/specs/execution.md`, `docs/specs/results.md`,
`docs/specs/targeting.md`, `docs/specs/mcp.md`. Brings the evidence stack to current gofresh
(real observation brackets, external directives, per-input attribution), moves machine-local
evidence out of the repo, makes every verdict and refusal actionable, and redesigns the MCP
face for its actual consumer: a token-conscious agent inside a harness.

- [x] 1. Triage gate; gofresh bump to current with observation-bracket API adoption
- [x] 1.5. Full lifecycle and UX analysis, before any code: the tool's life inside the dev loop — git states (dirty, staged, rebase, branch switch) against records and self-cache; run/verify response-size envelopes at campaign scale, bounded and paginated; token economy per MCP response; failure-path UX walked end-to-end. Deliverable: analysis doc reviewed by the user; coding chunks start only on that review.
- [x] 2. Machine-local evidence split: user cache directory keyed by repo root, per-entry files with atomic installs; findings document stays repo-committable
- [x] 3. Changed-scope residue: gomutant-owned artifacts never report as residue
- [x] 4. Compile diagnostics: the captured build stderr reaches ephemeral/probe refusal messages
- [x] 5. Findings lead with the record's unverifiable cause before open survivors (CLI and MCP)
- [x] 6. gofresh seam context: freshness errors carry subject, oracle, package, and the moved input at every wrap site; serving semantics become legible where they're consumed — the --force flag's "covers" defined in one clause at the flag (the pin spans symbol body, oracle closure, and runtime inputs), and every served/re-measured line says why (served: body and oracle unchanged / re-measuring: oracle closure moved) — observed agent runs reach for --force defensively precisely because that sentence is missing, defeating serving wholesale
- [x] 7. Oracle instability: attribute unverifiability to the responsible test and suggest an explicit target oracle excluding it
- [x] 8. Survivor execution evidence: per-candidate executed/not-covered evidence recorded and bucketed (never-executed / executed-and-passed / unstable-oracle)
- [x] 9. Agent-first MCP face: server instructions and tool descriptions that teach when to use what; responses restructured for token economy with next-action guidance; every long-running tool arms the progress-token seam from the engine's progress events (keep-alive against client tool deadlines, phase-labeled, the stipulator gate treatment), with a heartbeat cadence so no compile or execution stretch stays silent past a deadline plus long-run advisory (native-Tasks work stays parked on its issue)
- [x] 10. Output and remediation audit: every message earns its lines; mechanical remediations become offered actions instead of imperative instructions
- [ ] 11. Warm-path measurement after the bump; persistent-memo decision surfaced with numbers
- [ ] 12. Close-out gate
