# Attribute test instability without silently weakening the oracle

The reported campaign treated one flaky test as an oracle-wide instability:
a provisional kill was re-scored on a serial re-run (a FLIP), survivors carried
`[unstable-oracle]`, and confirmation cost spread across the affected work.
Current confirmation still reacts to a real flip at window scope. Distinguish
that conservative execution rule from the separate advisory attribution and
reuse-refusal labels; a label alone does not identify a defective test.

Field report (gitfs, v0.57.7): `TestIntegration_AutoCommitUsesExistingBranchTipAfterReopen`
flipped a provisional kill of an unrelated gc symbol. Root cause on
the consumer side: the automation engine never quiesces after a
commit (nineteen identical commits in 400 ms under a 20 ms timer), so
the test's ancestry assertion races the next commit — a product
defect the instability exposed. gomutant's surfaces reported the
instability only as the survivor flag and the FLIP line; nothing
named the test as the unstable element or its flip rate.

The historical request included automatically excluding the flipping test.
That is not licensed by current full-oracle@3: required groups run unsplit and
an altered oracle requires whole remeasurement. Stating a coverage cap alone
does not make the reduced oracle's result a verdict about the original oracle.

The retained obligation is to name the test, its actual flip evidence and the
scope affected, while keeping advisory work off other targets' commit path.
Reuse refusal, a volatile input, an incomplete probe or an unknown result is not
a flip. Preserve confirmation and full-oracle fallback; any explicit quarantine
workflow must keep the changed oracle's identity and results separate and must
not inherit prior outcomes by test-name matching.

Lands: tooling-redesign 6.3.
