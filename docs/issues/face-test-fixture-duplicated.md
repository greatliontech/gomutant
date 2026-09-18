# The two faces' tests build one module fixture and one evidence helper twenty-three times

Across internal/cmd and internal/mcpserver, the `example.com/current`
module (a go.mod, a source file, a test file) and the
`evidence(symbol) gomutant.SubjectEvidence` helper are written inline
in twenty-three tests, byte-similar, each pinning one face's shape of
the same run; the two render-bound pins add two more, near-identical
but for the entry call. One test-support package — the
internal/gitfixture precedent — holding the module and the evidence
builder collapses them, and a face-scenario table naming each face's
entry turns twin tests into one.

Lands: cross-tool train chunk 219 (the test surface).
