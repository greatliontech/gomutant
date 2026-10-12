# A full run pattern does not establish complete oracle admission

Lands: Gofresh docs/plans/tooling-redesign.md 5.1s, before relying on
Gomutant for the next consumer's assurance.

Three source-traced cases survive the full-oracle scheduling cutoff. They
are not negative-coverage exceptions, and their deciding regressions have
not yet been run.

## Go's runnable declaration inventory

`internal/engine/enumerate.go` requires a pointer element to be `*types.Named`.
`internal/engine/enumerationcheck.go` independently requires a bare `*T` to
come from a dot import. Go also accepts `type T = testing.T` followed by
`func TestAlias(t *T)`. Under alias-aware type information both Gomutant
inventories can omit a runnable assertion and agree with each other.

Use an ordinary passing test beside an alias-shaped test that alone detects
the mutant. Compare Go's actual selected inventory, derivation, explicit
selection and the independent lag check. Include Fuzz signatures and invalid
type/name controls without manufacturing an empty oracle.

## Effective selection at execution

The frozen environment preserves GOFLAGS. `environmentArms` does not refuse
its skip forms, and generated test arguments override `-run`, not `-skip` or
`-test.skip`. Baseline admission accepts a positive aggregate count and a
successful exit. With `TestWeak` beside `TestKiller`, inherited
`GOFLAGS=-skip=TestKiller` can omit the deciding test while the finding names
the full declared oracle and carries the current execution-policy label.

Pin both aliases, quoted argument grammar, effective repeated settings and
configuration sources on the campaign and ephemeral paths. Preserve explicit
build selection and legitimate in-test skip behavior. Refuse or account for
selection-changing configuration before measurement; a full pattern alone
does not discharge the obligation.

## A killer's own baseline disposition

`probeResult` retains a count and group success, while `attributedKill`
checks oracle membership. A test that skips on the original and fails on the
mutant can be named as a killer although it never passed the baseline, contrary
to REQ-exec-attribution. A separate passing test makes the baseline count
positive. Distinguish this from omitted execution, package failure and timeout
attribution; do not silently weaken the specified passed-baseline rule.

Carry actual named terminal facts through the existing execution account.
Establish the historical admission consequence: a policy label or old aggregate
count cannot supply missing selection or named-pass evidence. Preserve readable
history and complete-oracle execution, without re-enabling coverage-based
narrowing or unlicensed changed-oracle composition.

Until repaired, historical campaigns supply no new assurance. A hand probe is
useful only after its explicit deciding tests, effective selection and baseline
dispositions have been established independently. Shaped materialization has
separate isolation/dependency obligations and remains unavailable as assurance.
