# Child execution is missing from negative coverage exemptions

Lands: Pew performance-evidence plan chunk 12, before its final mutation gate.

`REQ-exec-oracle-run` permits narrowing only from sound batch coverage. A test
that reexecutes its own instrumented binary can kill a mutant in the child while
the parent's coverage profile records no reach. The mixed partition is unsafe:
another batch reaches the target, so the none-reaching full-group fallback does
not apply, and the child-owning test is excluded.

Pew's `TestInheritedMalformedEnvironmentRefusesBeforeListing` reexecutes
`os.Executable()` with a malformed environment and checks that `runGC` refuses
before listing. The child is the mutated binary, not a build of unmutated disk
sources. Both deletion and force-false mutations of `gc.go:41` are killed by
named ephemeral probes. Campaign `e8423761e551fb96` initially classified both as
narrowed survivors; its full-oracle audit killed the deletion and attributed it
to that test, while force-false remained a survivor. One sampled audit does not
establish the other exemptions.

The causal boundary is `internal/engine/coverage.go` collecting one parent
`-coverprofile`, `run.go` treating a nonintersection as sound negative coverage,
and `schedule.go` using that result to omit the batch. Descendant coverage is
neither collected nor established absent. Runtime-evidence merge refusal is a
separate reuse limitation and does not justify losing the killing test.

This needs a shared coverage-admission design, beyond Pew's observation adapter:
negative evidence with unaccounted-for child execution must not authorize an
exemption. Use the existing whole-group fallback unless completeness is
established; invalidate affected stored coverage evidence. A regression must
include both a child-owning killer and another in-process reaching batch.
Retain the complete oracle and remeasure affected survivors after repair; do
not attest inequivalent mutants, drop integration tests, or add purity vouches.
