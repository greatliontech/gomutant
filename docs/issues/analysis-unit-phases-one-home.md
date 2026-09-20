# The per-unit phase set is spelled by the consumer

`analysisUnitPhases` (grammar.go) is a closed list of gofresh's per-unit
progress phases — list, typecheck, load, hash, observe, runtime, prove —
the ones a keep-alive names a stretch for; gofresh's fact phases (served,
cancelled, budget-exhausted) and diagnostics name none. The list matches
gofresh v0.102.2 exactly, and it is pinned closed by
TestAnalysisEventStretchIsThePerUnitPhases. A gofresh release adding a
per-unit phase silently loses that phase's stretch on both faces — the
exact silence chunk 257 removed — and nothing here detects the drift,
while gofresh's fact phases are closed by construction (reported at an
operation's end, enumerated in Progress's doc).

The one home is the producer's: gofresh exports the classification
(`Progress.IsUnit()` or an exported phase set) and gomutant's Stretch reads
it; until then the bump checklist re-walks gofresh's emitters against the
list.

Lands: gomutant's bump behind gofresh 265/266 (the gofresh release that
exports the predicate — a rider on 265, the gotool/second-half release).
