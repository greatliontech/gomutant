# The two faces' tests build one module fixture and one evidence helper twenty-three times

The original survey across internal/cmd and internal/mcpserver found the
`example.com/current` module (a go.mod, a source file, a test file) and the
`evidence(symbol) gomutant.SubjectEvidence` helper written inline
in twenty-three tests, byte-similar, each pinning one face's shape of
the same run; the two render-bound pins added two more, near-identical
but for the entry call. One test-support package — the
internal/gitfixture precedent — holding the module and the evidence
builder collapses them, and a face-scenario table naming each face's
entry turns twin tests into one.

The current faces still carry their own fixture/evidence compositions. Re-census
the surviving callers after migration rather than treating twenty-three as a
fresh count. Share setup and builders, not the independent face assertions;
retain file isolation and actual producing evidence instead of fabricated
fingerprint fields or a test-local copy of a retired protocol.

Lands: tooling-redesign 5.6.
