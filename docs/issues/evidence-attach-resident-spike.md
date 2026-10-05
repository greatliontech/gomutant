# A target's evidence attach spikes the resident set past the proof pass

Measured 2026-10-05 over gomutant's own derived campaign (`run --changed
b2933b8`, 20 targets, 725 candidates, the machine alone, the process's
VmHWM sampled every fifteen seconds): the mode-wide proof pass grew the
high-water mark from 1.4 GB to 3.6 GB over seventeen minutes; then, at
the first target's confirmation — the rows `analysis observing oracle
runtime inputs (freshness evidence)` and `confirming target 2/20` — the
mark rose to 7.0 GB and 8.4 GB within three minutes, and stayed there.
The campaign's resident ceiling is the per-target evidence attach, not
the proof pass chunk 311 slices: the runtime-input observation of the
oracle process over gomutant's own oracle — the bracket's ingest of
what the process read — held several gigabytes for one target.

Measure the attach alone (one target, the ingest's size and the
manifest's) before deciding where the cost lives — gomutant's
withCandidateEvidence, or gofresh's runtimeinput ingest — and whether
it bounds (the oracle memory ceiling bounds the oracle processes, not
the parent's ingest of their observations).

Measured 2026-10-05 at 308's open (the same target alone, the parent
sampled every five seconds): warm, the attach raised the high-water
mark by 0.05 GB and the fold after it by 0.1 GB over a 0.54 GB run;
cold (a fresh gofresh cache home), the same run reached 3.47 GB inside
the proof pass before any attach. The attach's own cost is small; the
spike is gofresh's cold analysis holding every program a pass loads
until the pass ends — the campaign's 8.4 GB was the first target's cold
observation and fold under the state the run already held.

Lands: gofresh cross-tool train chunk 314 (the pass's program retention
bounded; gofresh docs/issues/cold-analysis-pass-program-retention.md
carries the measurement); gomutant's own half — the heap returned and
the server bounded — is chunk 308's.
