# Plan: hot-loop UX — current evidence stack, attributable verdicts, agents are first-class

Spec: `docs/specs/overview.md`, `docs/specs/execution.md`, `docs/specs/results.md`,
`docs/specs/targeting.md`, `docs/specs/mcp.md`. Brings the evidence stack to current gofresh
(real observation brackets, external directives, per-input attribution), moves machine-local
evidence out of the repo, makes every verdict and refusal actionable, and redesigns the MCP
face for its actual consumer: a token-conscious agent inside a harness.

- [ ] 1. Triage gate; gofresh bump to current with observation-bracket API adoption
- [ ] 2. Machine-local evidence split: user cache directory keyed by repo root, per-entry files with atomic installs; findings document stays repo-committable
- [ ] 3. Changed-scope residue: gomutant-owned artifacts never report as residue
- [ ] 4. Compile diagnostics: the captured build stderr reaches ephemeral/probe refusal messages
- [ ] 5. Findings lead with the record's unverifiable cause before open survivors (CLI and MCP)
- [ ] 6. gofresh seam context: freshness errors carry subject, oracle, package, and the moved input at every wrap site
- [ ] 7. Oracle instability: attribute unverifiability to the responsible test and suggest an explicit target oracle excluding it
- [ ] 8. Survivor execution evidence: per-candidate executed/not-covered evidence recorded and bucketed (never-executed / executed-and-passed / unstable-oracle)
- [ ] 9. Agent-first MCP face: server instructions and tool descriptions that teach when to use what; responses restructured for token economy with next-action guidance; long-run advisory on the run surface (native-Tasks work stays parked on its issue)
- [ ] 10. Output and remediation audit: every message earns its lines; mechanical remediations become offered actions instead of imperative instructions
- [ ] 11. Warm-path measurement after the bump; persistent-memo decision surfaced with numbers
- [ ] 12. Close-out gate
