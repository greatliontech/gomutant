# The window's probe phase is invisible to the cost model and the progress surfaces

A window's coverage probes run after the executing event and before
the estimate event, so for their whole duration the progress line
shows a frozen done-count and no projection exists yet. Measured in
the chunk-137 relaunch: window 1's probe phase (5 works x up to 3
groups of batched coverage passes against a ~19-minute suite) ran
about two hours before the first estimate or tick appeared — the
same "is it stuck?" ambiguity the ticks and estimates were built to
remove, reintroduced one phase earlier.

Probe cost is modelable from the same banked durations the window
projection uses (batch count = ceil(sqrt(N)); per-batch cost ~ suite
share + coverage overhead): the probe pass should announce its own
projected cost when it starts and tick per batch, exactly as the
mutant phase does.

Lands: with train chunk 136 (the consolidation/automation audit) or
the next change to the window cost model.
