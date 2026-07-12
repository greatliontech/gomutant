# Execution

Running a mutant answers one question: did an oracle test notice? gomutant
runs the target's oracle against each mutant and decides the outcome by a
rule strict enough that a noisy or corrupted run is refused rather than
scored.

**REQ-exec-oracle-run** (behavior): gomutant MUST run a target's oracle
against each of its mutants — in isolation, through the build overlay
([mutation.md](mutation.md)), never the whole test suite unless the oracle is
the whole suite — and report every mutant no oracle test killed as a survivor
carrying its source position and the operator that produced it. Scoping the
run to the oracle is what makes a survivor mean "the tests that vouch for this
symbol did not notice," rather than "no test anywhere noticed." An oracle
spanning packages is scoped per package — each package run with the test
pattern of its own oracle tests alone — because one union pattern would also
run a same-named non-oracle test in a sibling package, whose failure is
unattributable and aborts a sweep the per-package form completes.

**REQ-exec-attribution** (behavior): A kill MUST be one of exactly three
attributed events, enforcing REQ-core-attributed-kills: a named oracle test
reporting failure in the run's structured output; a timeout; or a
package-scope failure with no test-level event — admitted only when a
baseline probe of the unmutated tree passes, which distinguishes a
goroutine-panic-class kill from environmental noise. A run that fails in any
other way — a build error the overlay should have prevented, a killer test
outside the oracle, output that does not parse — aborts without recording a
finding, because a corrupted measurement read as a sound one inflates kills
in the flattering direction.

**REQ-exec-observation** (behavior): gomutant MUST capture one independent Go
testlog observation for every mutant and package-baseline process it launches,
finalize completed logs against that process's package working
directory, and merge their states as a deterministic union. The merged state is
attached conservatively to the target and every oracle subject in the finding.
A process that times out, panics, exits before normal test-harness completion, or
otherwise cannot prove its log complete contributes an explicit unverifiable
observation rather than an empty observation assertion. A stale or unverifiable
subject remeasures the finding; an incomplete log is never silently discarded.

**REQ-exec-ephemeral** (behavior): gomutant MUST run an ephemeral mutant — a
caller-supplied replacement of one source file, given whole or as exact-match
edits applied to the file's current content, exercised through a build
overlay against a named oracle test, the tree never touched — for the manual
mutations the operator set cannot generate (generated-data drift, resolver
seams, caller mappings). An edit that matches nothing, or matches more than
once, is refused rather than guessed: a mutation applied somewhere the
caller did not mean is a measurement of the wrong mutant. Before running the mutant gomutant probes the named
test on the unmutated tree: a `-run` matching zero tests cannot attribute any
outcome, and a test already failing clean would fail against the mutant too
and read as a fabricated kill — the flattering direction
REQ-core-attributed-kills refuses — so either probe result refuses the run
rather than scoring it. The result reports whether the named test killed the
mutant and the attributed failing test; it is evidence for the caller to act
on, never persisted to a finding record (REQ-result-record).

Reproducibility across runs is bounded by the oracle's own determinism: a
flaky oracle yields flaky kills, which is itself a finding about the tests.
gomutant does not promise identical survivors across runs — it promises that
an outcome it cannot attribute is refused (REQ-exec-attribution), so noise
aborts rather than scoring.
