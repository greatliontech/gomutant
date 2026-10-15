# Historical negative-coverage exemptions need sound authority before returning

The original report described a coverage exemption with a sampling blind spot:
a non-reaching batch was omitted, while the audit sampled narrowed survivors;
an extent attributed to no test was reported outside that sample too. The later
none-reaching-arm reproduction below did not establish that omission path.
Neither account supplies negative-coverage authority for the current tool.

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

Current full-oracle@3 has removed that authority: every required group runs
unsplit, and historical batch banks cannot restore omissions. Sampling an
unauthorized exemption is not a sound repair. Parent-profile absence proves
neither absent child execution nor absent compile-time influence.

The historical none-reaching arm (44abf11) already ran an empty-reaching
partition whole. The 2026-09-08 triage reproduced the report's class over a
switch on string tags: coverage blocks span the switch header and each
case body but never a case tag, so a tag mutant's extent intersects no
block — the campaign still killed the tag mutant (whole-group run) and
the only never-executed survivors were the untested arm's, correctly
prescribed coverage. The reported open survivor therefore reached its
verdict by a path the report does not carry.

Any future negative authority requires a complete harness/locality premise AND
candidate-influence proof, with full-oracle fallback and a positive arithmetic
control. Recover and remeasure the affected historical population; policy 1/2
records and their never-executed labels are history, never exemptions or
equivalence authority. Do not rebuild audits for the removed scheduler.

Lands: tooling-redesign 6.2.
