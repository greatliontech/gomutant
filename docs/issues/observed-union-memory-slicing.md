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

Measured 2026-09-30 at chunk 284's open over gomutant's own campaign
(`run --changed 9753a5e`, 81 targets, a derived union of 1,003 subjects
over 8 packages in one module), sampling the process's resident set
every fifteen seconds through the whole proof pass: peak 3.54 GB (VmHWM)
on a 123 GiB host, the pass ending inside the run's first eighteen
minutes; the campaign was stopped later, in its first baseline probe, so
the oracle processes' own memory (bounded by the oracle memory ceiling,
not this pass) went unmeasured. The low-memory stop the self-campaign
field report records happened on the same 123 GiB host as this
measurement, during a 45-minute-timeout rerun of the same campaign; this
measurement does not reproduce it, having ended before any concurrent
oracle execution — the phase where an aggregate of oracle processes, not
the proof pass, could have caused it. On this host and this union the
pass pays no resident cost worth a slice, so neither slice shape can be
measured for a wall benefit here and no shape lands on this evidence.

Lands: a proof pass observed to reach memory trouble — a consumer's
report of a pass whose peak resident set exceeds half the host's
MemTotal, or a pass the host stopped for memory — carrying the union's
subject and package counts and the host's RAM, which decide the slice
shape (a fixed count beside the budget, or a share of the host's
memory).