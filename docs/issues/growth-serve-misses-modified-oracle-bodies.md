# Growth serve misses modified oracle test bodies — false survivor served

VERDICT-INTEGRITY defect, field-proven (pando agent, 2026-08-22,
binary 4498b35): a survivor record survived an oracle re-measure
after the test that now kills it was STRENGTHENED IN PLACE.

The evidence chain: (1) run A measured Pager.flushBump 13/13 with one
open survivor (pager.go:695:44, arithmetic + -> -, executed-and-
passed) — correct then. (2) The author strengthened an EXISTING bound
oracle test (TestUnitBumpPacking gained the flushed-tail assertion
that discriminates the mutant) and added 5 new tests elsewhere in the
package. (3) Run B, no --force: the decision line read
"stale: derived oracle changed (added: 5 tests: ...)" — added only,
no modified — and the re-measure reported the SAME survivor still
open. (4) `gomutant ephemeral` on the identical mutant: KILLED by
TestUnitBumpPacking. Run B's record is false, and it sits in the
findings document as an attested-lookalike truth.

Hypotheses consistent with the evidence, to discriminate at fix time:
the survivor re-run under the monotone-growth carve-out executes only
the ADDED delta (or the pre-edit body), and/or the oracle-evidence
pin compares test-set membership without each test's source closure —
either way the documented pin ("every oracle test's source closure")
did not invalidate on a body edit.

Asks, all three: (a) a changed oracle test BODY invalidates the
target's pin exactly as an added test does; (b) when any existing
oracle test changed, survivors re-run against the FULL oracle, never
the added set alone; (c) the stale reason names modified tests
("modified: ...") beside added/removed. A regression net pins the
exact field scenario: strengthen-in-place plus unrelated additions in
one edit, survivor must flip.

Reporter's workaround: --force on the affected symbols (logs:
~/.claude/jobs/038121fe/tmp/targeted-run2..4.txt).

Lands: user decision (flagged for immediate fix: every findings
document measured under the growth carve-out since it shipped may
carry false survivors from strengthen-in-place edits; the fix should
state the re-measure guidance for standing stores).
