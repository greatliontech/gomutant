# Kill confirmation re-executes serially, ignoring the stability evidence it guards with

Lands: gomutant train, second item — after the preflight/progress
pass.

## Observed (field run)

Every non-timeout kill re-executes alone, serially — the run's dominant
cost, scaling with kill count, so a well-tested codebase pays the most.

## Direction

Confirmation exists to guard against load-flaky kills, and per-test oracle
stability is already measured (the same records that flagged the
unverifiable targets). Gate confirmation on that evidence: a kill delivered
by a measured-stable oracle skips solo re-execution (or is sampled);
full serial confirmation reserved for flaky-history oracles and
timeout-adjacent verdicts. Verify the current confirmation shape at fix
time against the field observation.
