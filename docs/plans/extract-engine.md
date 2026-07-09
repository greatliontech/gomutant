# Plan: extract the mutation engine from stipulator

Spec: docs/specs/{overview,targeting,mutation,execution,results}.md

- [x] 1. Spec reconciliation: the corpus predates stipulator's hardening advances — walk stipulator docs/specs/hardening.md and the landed features against this corpus; fold in what is engine-domain (ephemeral one-file mutants, attestation position rebasing, toolchain platform pin, record unknown-field tolerance, changed-symbol surface as a targeting producer), leave what is binding/gate-domain, and record the boundary per clause.
- [x] 2. Symbol layer: Go tree loader (module + go.work), symbol→FuncDecl resolution ("pkg.Func" / "pkg.Type.Method"), canonical body hash (REQ-result-record's body-hash leg; grammar shared with gofresh/stipulator).
- [x] 3. Operators: mutant generation over a resolved body — full REQ-mut-operators set with dedup/no-op discard, operator-set version, per-symbol budget (REQ-mut-budget).
- [x] 4. Execution: overlay-isolated mutant runs (REQ-mut-overlay), oracle-scoped `go test -json`, three-event kill attribution with baseline probe, rapid-property split, timeout-as-kill, ephemeral one-file mutant runs with the unconditional baseline probe (REQ-exec-oracle-run, REQ-exec-attribution, REQ-core-attributed-kills, REQ-exec-ephemeral).
- [x] 5. Targeting: the target model, default package-tests oracle, auto-discovery producer, config-file producer, changed-scope discovery with the untargeted-residue report (REQ-target-model, REQ-target-producers, REQ-target-oracle, REQ-target-default, REQ-target-labels, REQ-target-changed).
- [x] 6. Results: pinned finding records, staleness re-measure, survivor attestation, versioned export document with unknown-field tolerance (REQ-result-record, REQ-result-stale, REQ-attest-survivor, REQ-result-export, REQ-result-findings, REQ-result-tolerant).
- [x] 7. CLI and the ephemeral API: cmd/gomutant — run / findings / attest / ephemeral over the library; the composed REQ-exec-ephemeral operation exposed (chunk 4 delivered only its probe leg).
- [ ] 8. stipulator swap: targets export on the stipulator side, gomutant dependency, delete the extracted engine from internal/harden + backends/golang, rewire ephemeral/staged/reminder atop gomutant.
