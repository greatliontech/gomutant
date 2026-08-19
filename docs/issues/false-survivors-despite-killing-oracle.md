# False survivors recorded despite a killing package oracle

A full `--changed` campaign on tugboat (2026-08-19, 39 targets,
killer-scoped confirmations, standard vouches) recorded as survivors
mutants that the package oracle demonstrably kills:

- `placement.Driver.Evaluate` `placement.go:320:7`
  (loop control: break -> continue) — recorded `executed-and-passed`;
  `gomutant ephemeral` with the identical edit against the same tree
  and `--run Test` kills it (twice independently: the change-set
  reviewer's probe and the gate's own), the killer being a test
  (`TestEvaluateReportAccounting`'s draw anchor) present in the tree
  the campaign measured.
- `placement.DecisionKind.disruptive` `placement.go:86:33`
  (boolean operand -> false) — recorded `executed-and-passed`;
  ephemeral kills it via the ceiling-count test, also present at
  campaign time.
- `placement.Driver.Evaluate` `placement.go:325:10/:40` recorded
  `never-executed` although a test present at campaign time
  deterministically drives that branch (the dead-context arm).

Same tree, same package, same nominal oracle — the campaign's
per-target execution evidently ran a narrower effective oracle than
ephemeral's `--run Test` (candidate hypotheses to investigate: the
per-target derived-oracle attribution missing internal-variant tests
for SOME targets while including them for others in the same package
— sibling targets' new-test kills DID land — or a window run-regex
narrowing). A false survivor is conservative for adequacy but
poisons dispositions: equivalence attestations and redeferrals get
spent on mutants that are already dead.

Evidence: tugboat campaign log (gomutant-campaign6), the two
ephemeral kill outputs, findings doc at tugboat 482e8ad^..; the
tugboat gate recorded the affected mutants as probe-killed rather
than working them.

Lands: with the next gomutant execution-path change, or immediately
if a second campaign reproduces the class.
