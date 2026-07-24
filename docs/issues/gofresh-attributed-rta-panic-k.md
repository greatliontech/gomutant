# Changed-scope run panics in gofresh attributed RTA: `panic: K`

`gomutant run --changed HEAD` (CLI, v with gofresh v0.34.0) crashes deterministically during
the prepare/freshness phase on the cerebro repository — twice in a row, same stack:

```
panic: K

goroutine 1 [running]:
github.com/greatliontech/gofresh/closure.(*attributedRTA).addRuntimeType(...)
        gofresh@v0.34.0/closure/rta.go:389
github.com/greatliontech/gofresh/closure.(*attributedRTA).visitFunc(...)
        gofresh@v0.34.0/closure/rta.go:196
github.com/greatliontech/gofresh/closure.analyzeAttributed(...)
        gofresh@v0.34.0/closure/rta.go:264
github.com/greatliontech/gofresh/closure.attributedReachableSets(...)
        gofresh@v0.34.0/closure/tier2.go:930
github.com/greatliontech/gofresh/closure.(*Hasher).ComputeObservabilityBatch(...)
        gofresh@v0.34.0/closure/tier2.go:663
github.com/greatliontech/gofresh.(*View).ensurePrecise(...)  view.go:1538
github.com/greatliontech/gofresh.(*View).CaptureObservedBatch(...)  view.go:533
github.com/greatliontech/gomutant.(*Tree).newSubjectViewsWithPackageContext(...)  freshness.go:161
github.com/greatliontech/gomutant.(*Tree).Run(...)  run.go:695
```

Context: ~49 changed symbols across five packages (`internal/duties/iva`, `internal/engine`,
`internal/books/iva`, `internal/legalhistory`, `internal/sim`) on a staged-but-uncommitted
tree; the last prepared symbols before the panic were `sim.Scenario.Run` /
`sim.Scenario.WithLegalHistory`. The delta introduces new generic instantiations
(`temporal.Series`-building generic helper, `profile.NewKey` instantiations over new struct
types) — `addRuntimeType` panicking with a bare one-letter message suggests an unhandled
runtime-type shape in the attributed RTA rather than a capacity or IO issue.

Same run earlier attempted over MCP died at the client transport ("Connection closed"), so
the CLI is the only observation channel and it cannot complete. The per-chunk measured run is
blocked on this repository until the panic is fixed; ephemeral probes (hand-written mutants)
keep working and were used as the chunk's mutation evidence.

A package-scoped run (`--package github.com/candosa/cerebro/internal/duties/iva`) panics
identically, so scope narrowing is not a workaround.

Repro: cerebro repo at the staged derived-IVA-group-profile change set,
`gomutant run --changed HEAD --jobs 0`.
