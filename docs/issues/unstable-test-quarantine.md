# One unstable test degrades the whole oracle; quarantine the test instead

A single flaky test in a derived oracle currently taints every verdict
that oracle produces: a provisional kill it causes is re-scored on a
serial re-run (a FLIP), every survivor in the group carries
`[unstable-oracle]`, and the campaign spends serial confirmations on
the whole group. The defect is the test's, not the oracle's; the
oracle-wide treatment turns one bad test into a cost and a doubt on
every mutant in the package set.

Field report (gitfs, v0.57.7): `TestIntegration_AutoCommitUsesExistingBranchTipAfterReopen`
flipped a provisional kill of an unrelated gc symbol. Root cause on
the consumer side: the automation engine never quiesces after a
commit (nineteen identical commits in 400 ms under a 20 ms timer), so
the test's ancestry assertion races the next commit — a product
defect the instability exposed. gomutant's surfaces reported the
instability only as the survivor flag and the FLIP line; nothing
named the test as the unstable element or its flip rate.

Asked: quarantine per test, not per oracle — when a test's verdict
flips between runs of the same tree, record that test as unstable
with its flip evidence, exclude it from the oracle for the rest of the
campaign with the exclusion on the run surface and in the document,
and keep the remaining tests' verdicts sound rather than flagging
them. The consumer's obligation stays: the surfaced test is a defect
to fix, and the exclusion is a stated coverage cap, never a silent
one.

Triage (2026-09-12, chunk 244's open gate): a per-test exclusion
narrows the oracle for the rest of the campaign — a mutant killed only
by the excluded test becomes a survivor under a stated cap — so the
shape is a verdict-affecting narrowing whose acceptance is the user's;
what is derivable today (naming the flipping test with its flip
evidence on every face) rides the same design.

Lands: user decision
