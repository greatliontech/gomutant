# ephemeral's three blind spots: state them in the guidance, and refuse rather than "survive"

Consumer report (bldc, 2026-08-30 and 2026-08-31, three occurrences
of one class): an ephemeral mutant builds elsewhere and the tree stays
untouched by design, so any oracle that observes the TREE rather than
the linked binary sees unmutated sources and can never kill. Met
three ways:

1. A source-reading guard test (a repository-root test parsing the
   module's own files via `os.ReadDir`/`go/parser`) — the probe
   reports `survived` with the `unexercisedFiles` advisory, whether
   or not the guard works.
2. A `go list`-based guard (import-graph layering tests) — the
   overlay is invisible to the child `go list`, so both weakening the
   denylist and adding a forbidden import report `survived`.
3. A `_test.go` target — by the tool's own policy ("test file: tests
   are oracles, never targets", gomutant.go) the probe carries no kill
   evidence, but the ephemeral result reads as a survivor rather than
   a refusal.

In every case the reading is "survived, and here is why that means
nothing", and a probe author who does not read the advisory takes a
vacuous survivor as evidence. Two asks, either sufficient for cases 1
and 2, the second for all three: treat a target file that no measured
test compiled as a REFUSAL (not a survivor — `unexercisedFiles`
already carries the signal, it rides survivors only), and name the
three blind spots in the ephemeral verb's guidance so authors route
such guards to review or self-vacuity floors instead of mutation.
The reachable workaround today for cases 1–2 — mutating the guard's
own INPUT (a kind added to the scanned set, an edge added to the
parsed table), which does link into the binary — is worth stating
beside them.

Lands: cross-tool train chunk 140 (gofresh docs/plans/cross-tool-train.md; triaged 2026-09-03).
