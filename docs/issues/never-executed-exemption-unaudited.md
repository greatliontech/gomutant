# A never-executed verdict is exempt from execution AND from the audit, so a coverage miss is never re-scored

The narrowed survivor's ground is measured batch coverage over the
mutant's extent: a non-reaching batch is exempt from execution, and
the standing audit re-scores a sample of NARROWED survivors under
the full oracle. A mutant whose extent the coverage attributes to
NO test lands in the never-executed bucket — exempt from every
batch and, being outside the narrowed class, outside the audit
sample too. When the attribution is wrong, nothing catches it.

Observed (v0.52.0, downstream workspace wisp, a staged
`--changed=HEAD` campaign, oracle 59 tests across one package):
`tools/platgen.emitNontreeSubscribe` line 2772 (the bool decode arm)
recorded as an open survivor bucketed never-executed; an
`ephemeral` probe of the same line (`case ... "bool"` → `case false`)
with `--run TestNontreeEmittedChainTypeChecks` — a test the
campaign's own decision line lists in the derived oracle — is
KILLED (the emitted domain no longer type-checks). The campaign's
audit lines report 0 disagreements across 13 windows because the
sampled class excludes this bucket. Dozens of sibling lines the
same test demonstrably executes carry the same verdict.

Two remedies, either sufficient: sample the never-executed bucket
into the audit exactly as the narrowed class is sampled (a
disagreement there is the loudest possible signal — the exemption
ground itself failed), or restore the full-run requirement for an
extent whose attribution is empty rather than treating emptiness
as a sound non-reach.

Lands: user decision
