# CLI runs silent for hours between the measure list and completion

Lands: when gomutant work resumes after gofresh's analysis-simplification
plan closes.

## Observed (field run)

Two-plus hours with zero output; phase had to be inferred from load
average and /proc.

## Direction

Periodic progress lines from the run loop (target n/N, confirmations
k/m, current phase) — the library already emits Progress/Decision events;
the CLI drops them. Cheap, and answers the operator's questions from the
log alone.
