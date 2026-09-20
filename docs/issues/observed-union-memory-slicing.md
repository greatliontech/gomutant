# The observed union's resident set scales with its package set

The observed union's proof pass (`subjectViewSet.observed` → gofresh's
`CaptureObservedBatch`) runs under one `closure.Hasher` per module view for
the whole pass, and the Hasher holds every loaded package program until the
pass ends: the resident set scales with the union's package set. Measured
2026-09-20 over pb's `ExtractZip`+`writeMember` derived union (486 subjects):
peak VmHWM 2,111,380 kB; the field report's 1,608-subject union reached
8 GiB on a 30 GiB host.

The analysis budget (`analysis_budget_sec` / `--analysis-budget`) bounds the
pass in time and so in memory growth, but the resident set of a pass that
finishes inside its budget is still the union's whole package set.

The option: slice the observed capture per package group above a stated
subject count, each slice its own Hasher (programs released between slices;
the persistent memo serves shared folds and proofs across slices), at the
price of the typed load repeating per slice — a wall-clock cost stated in the
run's pricing line. Two shapes to decide between: a fixed slice size (a knob
beside the budget) or a slice derived from the host's memory (the oracle
memory ceiling's derivation, RAM/(2 × jobs), applied to the analysis).

Lands: user decision — the trade is throughput (repeated typed loads) against
the resident bound, and the derivation's shape is a product choice.
