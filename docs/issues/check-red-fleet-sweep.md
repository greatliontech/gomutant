# stipulator check red: two requirements uncovered (fleet sweep 2026-10-12)

The weekly fleet sweep's `stipulator check` over this repo fails: five
red rows, two of them violations no gap excuses —

- REQ-mut-comprehensive-basis — bound witness
  `internal/engine.TestArithmeticCatalog` classified example: no
  property driver or analyzer call in the bound body (+11 more);
- REQ-result-candidate-conservation — bound witness
  `TestExtendFindingCountsAppendsSuffixOutcomes` classified example
  (+6 more).

Both clauses want property or analyzer evidence and hold example
witnesses only. The other three rows (REQ-core-attributed-kills,
REQ-result-export, REQ-target-model) are the excused uncovered rows
that rode every passing sweep and are not this doc's. The 2026-10-05
sweep read `pass — 0 unexcused` over three rows, so the two
violations are new in the interval: undiagnosed. The landed
negative-coverage cutoff (tooling-redesign 1.1–1.4: scheduling probes
and modeled savings removed, the positive arithmetic narrowing control
retained, historical banks and findings barred from granting reuse)
moved exactly the mutation-basis and candidate-conservation surfaces
these bind, which is the place to look first; this doc records the
fact, not the cause.

The current policy is `gomutant/full-oracle@3`: neither a historical count nor
an example-only witness establishes the property/analyzer obligation. The
reported reds have not been re-executed for this document update. Diagnose the
actual binding classifications and preserve the clauses' evidence bar; deleting
the remaining witnesses is not a resolution.

Secondary constituent: tooling-redesign 5.6 retires the obsolete helper tests
tracked in [retired-oracle-drift-helper-tests.md](retired-oracle-drift-helper-tests.md),
while preserving candidate conservation on admitted paths.

Lands: tooling-redesign 5.4.
