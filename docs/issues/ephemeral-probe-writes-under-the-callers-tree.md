# An ephemeral probe's tests write under the caller's tree

`Lands: user decision` — the isolation shape is a fork the spec's own
cost accounting owns: routing every probe's oracle through the scratch
channel REQ-mut-overlay already runs shaped candidates through (a
disposable copy of the tree per probe, its cost stated per probe, the
tree promise kept whole), or narrowing REQ-mut-overlay's run-time
purity to the reproducer clause it enforces today (the promise reads
"a mutant run disables reproducer persistence", never "never writes").

## The live half: any relative write of a probed test lands in the tree

The probe runs `go test -overlay <file> …` with the tree root as the
process directory (`internal/engine/run.go`, the ephemeral executor).
Under an overlay the test binary's working directory is the REAL
package directory — `go test` sets it so relative `testdata` reads
resolve — so a probed test that writes any relative path writes into
the caller's working tree by construction, for the probe's duration or
beyond it. REQ-mut-overlay promises the opposite: "a mutant run must
not write into the tree through the tests it executes either". Only
one writer is disabled today, rapid's failfile (below); every other
relative write is reachable. Demonstrated 2026-09-08 during the
chunk-160 review: a probe of a then-staged root test that resolved a
relative fixture path wrote `.gomutant/` under
`internal/engine/testdata/fixturemod` in the reviewer's checkout.

## The closed half: rapid's failfile (filed 2026-09-03/04 as `ephemeral-rapid-failfile-in-tree`)

Triage 2026-09-08 (cross-tool train chunk 160): the ephemeral path has
passed `-rapid.nofailfile -rapid.seed=1` to every property oracle since
2026-08-11, before both observations below. Two probes — a property
test importing rapid directly, and one reaching the runner through an
in-module helper package — were killed with the seed pinned and no
`.fail` file under the tree after the probe; a during-probe check was
not made (the live half above covers every transient write). The rapid
failfile writer is suppressed; the reports below reproduce only on a
gomutant older than that, or through the live half above (a test of
the consumer's own that writes relatively).

Observed 2026-09-03 in bldc (consumer report): two `.fail` files after
two probes of a rapid property in `internal/compile/flow`; gitignored
there, so no commit was polluted, but a stale failfile is a replay seed
the next honest run picks up, and an un-ignored repository would see
it as an untracked change. Second observation (bldc, 2026-09-04): even
where no failfile survives the probe, a file written into the caller's
`testdata/rapid` for the probe's duration is seen by another tool's
cache keyed on that directory's contents (stipulator's runtime-input
fingerprint), which re-executed its witnesses.

Asks: run the probe's tests with their working directory outside the
caller's tree (the scratch channel), or at least detect and remove
files created under the caller's tree during a probe and say so in the
result.
