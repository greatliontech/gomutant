# go1.27 generic-method shape breaks closure reachability ("unsupported analysis shape: Int")

Filed from a protodb close-out campaign, 2026-08-23. Binary
v0.40.2-0.20260823075506-a1f2e635bb48, rebuilt on go1.27.0-X:nodwarf5
— the failure is in the ANALYSIS, not a stale-binary parse (the same
campaign on the go1.26-built binary additionally failed to parse the
go1.27 stdlib; the rebuild cleared the parsing, not this). The likely
root is the shared gofresh analysis layer: pew (gofresh v0.76.0)
fails with the identical signature on the same tree (pew
docs/issues/go127-generic-method-closure-analysis.md), and this
binary rides gofresh v0.82.0 — a fix lands in gofresh and both tools
bump.

## Symptom

`gomutant run --changed <ref>` over protodb: 87 targets, 8 measured,
**79 skipped**, every skip:

    decision evidence unavailable: closure: attributed reachability:
    unsupported analysis shape: Int

The skipped set is exactly the targets whose oracle closure reaches
`math/rand/v2` (the DST/testharness world — go1.27's rand.v2 carries
the new generic-method shape at rand.go:213, `Int[T]`; the go1.26
binary's parse errors named the same site: "method must have no type
parameters"). The 8 measured targets' closures avoid it.

MCP-side calls (`findings`, `explain`) from a server still running a
go1.26-built binary fail harder — go/types errors over the go1.27
stdlib — that half is deployment (restart the server on the rebuilt
binary), not a bug.

## Impact

Any consumer module on go1.27 whose test closure touches
`math/rand/v2` (in practice: anything using testing/synctest-era
harnesses or seeded generators) loses campaign coverage for those
targets wholesale — 91% of the changed-target set in this instance.
The skips are honest (named, counted), but the campaign cannot
discharge its close-out obligation.

## Repro

On a go1.27 toolchain, any module with a test importing
`math/rand/v2`, e.g.:

    gomutant run --changed HEAD~1   # target whose oracle closure imports math/rand/v2

## Ask

Teach the attributed-reachability analysis the generic-method shape
(go1.27), or degrade per-shape (skip the unanalyzable EDGE, keep the
target measurable with a widened oracle set and the imprecision named
on the record) rather than skipping the whole target.

Lands: with the gofresh generic-method analysis fix (the shared
engine owns the failure; pew's twin filing rides the same fix), then
a gomutant bump to the fixed gofresh.
