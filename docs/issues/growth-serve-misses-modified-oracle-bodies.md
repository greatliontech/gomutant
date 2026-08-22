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

Hypotheses, revised after a source walk (2026-08-23): the original
serve-scope hypothesis is REFUTED — the derived oracle is the whole
package's test set (runPreparation.oracle → testsOf, not
coverage-filtered), the decision was a full MEASURE (not a growth
serve), and gofresh's compartment ledger provably marks body edits
non-inert (closure/testvariant_test.go pins it). So run B re-measured
all 13 candidates against an oracle that INCLUDED the strengthened
test — and still recorded the survivor, with an executed-and-passed
bucket, while ephemeral kills it. Remaining candidates, for the
reproducer to discriminate: (a) kill-then-confirmation demotion — the
initial suite run kills, the serial confirmation re-runs a
mis-attributed killer solo, passes, and the flip demotes to survivor;
(b) an oracle-invocation scope defect (the -run regex or failfast
interaction dropping the killer for exactly this mutant's schedule);
(c) a stale build/overlay serving the pre-edit test body to the
mutant processes despite the fresh load. The reproducer is the field
scenario verbatim: measure with a surviving mutant, strengthen the
existing killer-to-be in place plus unrelated additions, re-measure,
assert the flip.

Asks, all three: (a) a changed oracle test BODY invalidates the
target's pin exactly as an added test does; (b) when any existing
oracle test changed, survivors re-run against the FULL oracle, never
the added set alone; (c) the stale reason names modified tests
("modified: ...") beside added/removed. A regression net pins the
exact field scenario: strengthen-in-place plus unrelated additions in
one edit, survivor must flip.

Reporter's workaround: --force on the affected symbols (logs:
~/.claude/jobs/038121fe/tmp/targeted-run2..4.txt).

Reproduction attempts (2026-08-23): the field scenario verbatim at
the library tier — weak-oracle survivor, strengthen-in-place plus an
added sibling, fresh load, prior passed — PASSES: the survivor flips
to killed (TestRunSurvivorFlipsWhenExistingOracleTestStrengthens,
now a standing regression net). The field state itself is
unrecoverable (the pando session's tree was never committed and its
job directory is gone), so the divergence lives in a variable the
minimal rig lacks: the Store/CLI path over a 28 MB document, the
prior's candidate-evidence rows, the confirmation stride under the
single-vouch set, or a schedule-dependent kill-then-confirmation
demotion. Watch posture until the next occurrence: the reproducer
stands guard; the progress/exit-reporting issue's
confirmation-mode line and a LOUD log line on any
kill-demoted-by-confirmation event are the instrumentation that
makes the next field hit self-diagnosing.

Lands: user decision (the next field occurrence with retained state,
or the confirmation-demotion instrumentation surfacing a hit).
