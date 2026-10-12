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

Scanned: the index and all 53 docs it lists. None tracks the check
or names either requirement. retired-oracle-drift-helper-tests
(Lands: tooling-redesign 5.6) names candidate conservation as a
surface its fixture retirement must preserve on the surviving paths;
its rows are the helper fixtures, not these two verdict rows, so the
sets are disjoint — and that retirement must not close this doc by
deleting the remaining witnesses. The 2026-09-21 check-red doc closed
at chunk 278 and is gone.

Lands: tooling-redesign 5.4 — gomutant's next chunk in the active
sequence; the two requirements are re-covered there with the evidence
class their clauses demand, or gapped with a stated reason. The
sweep's next gomutant verdict reading `pass` closes it.
