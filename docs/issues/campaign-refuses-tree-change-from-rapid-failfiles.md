# Campaign refuses mid-run when killed mutants write property-test failfiles

## Symptom

A campaign over a tree whose test suite includes pgregory.net/rapid
property tests aborts partway with:

    gomutant: tree changed under measurement: <pkg>.<sym>: gofresh:
    analysis view changed: closure for <pkg>.<sym> (and N more
    targets); M completed target(s) kept in the findings document;
    re-run to measure the refused set

Re-running reproduces the refusal at the same closure: the refusal
is self-inflicted and permanent, not transient.

## Mechanism

A mutant that a rapid property test kills makes rapid persist the
failing bitstream as `testdata/rapid/<Test>/<Test>-<stamp>.fail`
inside the package source directory. gofresh's analysis view hashes
the package closure including testdata, so the first property-killed
mutant of any target whose closure contains such a package poisons
every later target sharing the closure. Observation brackets do not
help — they cover runtime inputs, not the analysis view.

Observed in ocifs (2026-08-16): campaign completed 59 targets, then
refused the ~25-target fusefs closure twice in a row; the closure
contains internal/projection and internal/fusefs, both carrying
rapid suites with testdata/rapid directories.

## Possible directions (author's call)

- Exclude `testdata/rapid/**` (or a configurable glob) from the
  analysis view — the dominant convention for a directory tests
  write to by design.
- Or: snapshot the analysis view's testdata state per-target and
  treat growth of rapid failfiles as oracle scratch, not source
  change.
- Or: run mutant oracles with rapid's failfile persistence disabled
  (if rapid grows such a switch) — weakest, since it changes test
  behavior under measurement.

Lands: author's triage.
