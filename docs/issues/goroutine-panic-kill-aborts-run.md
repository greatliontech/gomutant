# A goroutine-panic kill is classified as environmental noise and aborts the run, discarding every completed measurement

Lands: when mutant-failure classification next distinguishes kill shapes, or
with the first fix pass over run-abort semantics.

## Observed

Live instance (tugboat, `transport/grpctransport`): a `statement: delete`
mutant removed the `if stream == nil { ... }` guard in a function that owns a
per-peer goroutine. Under the mutant, the goroutine dereferences the nil
stream, the panic crashes the whole test binary, and `go test` reports a
package-level failure whose panic traceback roots in the goroutine spawn —
no single test function is attributed. gomutant classified this as:

```
mutant run failed with no test-attributed kill (environmental noise, not a
kill; baseline probe did not clear it): exit status 1
```

and aborted the entire run. Two distinct problems:

- **Classification.** The oracle demonstrably detects the mutant — the suite
  cannot pass under it; the baseline probe passes without it. A
  process-crashing panic caused by the mutation is the strongest kill shape
  there is. Requiring per-test attribution makes any mutation whose effect
  surfaces in a spawned goroutine (nil derefs behind deleted guards, closed
  channels, double-unlocks) unmeasurable in exactly the packages that most
  need mutation coverage — concurrent runtime code.
- **Abort semantics.** The single unclassifiable mutant aborted the run and
  nothing was committed: eleven other symbols had fully measured (80+
  candidates each) and their results were discarded. The caller's only
  recourse is re-running with symbol filters excluding the offending
  function, then measuring that function by hand.

## Resolution

- Classify a mutant-run failure as a kill when the baseline probe passes and
  the failure reproduces under the mutant, regardless of test attribution;
  keep the attribution field optional ("killed by: package crash (goroutine
  panic)"). If a conservative tier is wanted, record it as
  `killed-unattributed` rather than refusing the verdict.
- On a genuinely unclassifiable mutant, record that one candidate as
  unverifiable with the diagnostic and continue the run; commit everything
  that measured. An abort that discards completed work should be reserved
  for corrupted state, not one odd mutant.
