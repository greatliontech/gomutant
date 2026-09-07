# Make Measured, Committable, And Reusable Findings Distinct At The Run Surface

Lands: user decision

## Consumer Evidence

Stash's verification work used gomutant v0.55.0 with GoFresh v0.98.0 and the
ambient `go1.27.0-X:nodwarf5` toolchain. An explicit live-state campaign completed
with three repository-layer findings and 77 killed mutants. The individual
`internal/state.environ` finding recorded 19 kills. An immediate freshness
inspection of the unchanged tree classified the findings as unverifiable:

```text
target: package graph shares mutated dynamic state:
github.com/thegrumpylion/stash/internal/sh/expand.zeroConfig escapes writable
```

History, editor, and prompt findings instead reported:

```text
target: subject accepts caller-supplied dynamic behavior
```

The same distinction appeared during survivor attestation: the disposition was
recorded, followed by an unverifiable-record warning. The repository findings
already carried `observationObservable: false`, including startup/unaudited
operation reasons. No positive proof is shown to have been lost. Recorded
consumer provenance is Stash local revisions `f241458` and `5b53500`; those hashes
identify the source observations, not a prerequisite for the reduction below.

The useful wrinkle is that the run summary presented completed counts and
`layer: repo`, while discovering the non-reusable posture and its explanation
required another operation. A consumer initially mistook committability for
current reusable evidence. The different stored-observation and later judgment
reasons also need their respective channels named.

## Existing Semantics To Preserve

The overview and `docs/specs/results.md` deliberately separate completed
measurement, record locality, and freshness. Negative observability can be
captured and validated as the same disposition; that does not turn it into a
positive reusable proof. `freshness.go` captures the observed batch and runtime
union, whereas the judge calls `CheckObserved`. `store.go` routes committability
without requiring a positive observability verdict.

Shared dynamic state and caller-supplied behavior are conservative refusal
classes, not established false positives in this report. An ignored
`context.Context` parameter on a method is sufficient for the signature-shaped
openness classification. Do not fix the presentation by admitting unproven
behavior, adding blanket purity assertions, or making every measured row current.

## Reduction For The Owning Agent

The following is a source-derived reduction, not an executed reproduction. Put
this method and a table test for negative, zero, and positive inputs in a clean
Go 1.27 fixture module, then explicitly select the method and its test:

```go
type Store struct{}

func (Store) Value(_ context.Context, n int) int {
    if n > 0 {
        return n
    }
    return 0
}
```

Compare the run result, persisted observation disposition, immediate
`gomutant findings --judge`, and survivor-attestation response under unchanged
inputs and selection. This isolates the ignored-interface-parameter/method
boundary without Stash, subprocesses, or the shared `zeroConfig` variable.
Retain a separate fixture with a writable shared configuration to pin that
different refusal channel.

## Requested Resolution

- Distinguish newly measured outcomes, repository/machine locality, and known
  reuse refusal in human and machine run/attestation surfaces; do not require a
  second judgment merely to expose a refusal already captured by the run.
- Name the stored observability and current freshness channels when their reasons
  differ; describe whether a later judgment needs additional analysis.
- Preserve valid measured kills and equivalence dispositions without presenting
  them as reusable evidence or weakening conservative freshness checks.
- Correct the package-only default-oracle wording in `README.md:16` and the
  private `resolveOracle` comment at `gomutant.go:687-690`:
  `REQ-target-default` actually includes runnable tests in every in-tree package
  whose test binary links the target
  package. Stash's inferred history oracle consequently included CLI/PTY/app
  tests. This is intentional selection, not evidence of a too-broad selector.
- Explain that explicit inventories override that derivation; observation
  brackets for actual `/bin/cat` and `/bin/sh` use are legitimate, and resulting
  app findings can remain machine-local. Do not silently remove integration
  oracles to make a report look more reusable.
- Correct the separate public `DiscoverContext` comment at
  `gomutant.go:413-418`, which says initializers cannot be resolvable targets.
  `internal/engine/enumerate.go:52-55` emits their supported positional identities
  and `targeting_test.go:551-575` checks them. A non-generated `wired.go` with
  `func init()` is targetable as `<pkg>.init#wired.go#0`; this is documentation
  drift, not a request to expand targeting semantics.

The [consumer admission report](vmm-finding-admission-refusals.md) also carries
Stash's GoFresh audit-key adoption requirements. That source repair does not
establish that all dynamic-state or callback refusals disappear. This issue
owns the run/reuse presentation distinction, not a second adoption mechanism.
Stash measurement remains blocked on the upstream resolutions; the owning agent
will assign this issue to its roadmap.
