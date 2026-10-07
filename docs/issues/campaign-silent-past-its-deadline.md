# A campaign silent past its deadline, killed by the shell

Lands: cross-tool train chunk 267 (slotted at audit 333: the stopped-reporting half is 267's own rep.stop()-before-ledger.Finish ordering, cmd/run.go:375 vs :398; 267 opens by reproducing the deadline half against REQ-exec-cancellation — fixed there or refuted with the reproduction; pew docs/issues/mutation-oracle-observation.md is the same campaign's other half).


pew's `mutation-oracle-observation.md` (pew 044f7d8) reports a
changed-code campaign over newStatCmd, validateOptions and Compare that
banked three machine-local records, then stopped reporting progress
about eight minutes into a ten-minute command deadline and was killed
by a thirty-minute shell wrapper during "final observation/deadline
work". The pew half (its derived oracle's CLI tests change the working
directory, so observation refuses `relative runtime input after
working-directory change: cmd/pew`) stays with that plan's chunk 4; the
gomutant half — a run that reaches its deadline, stops reporting, and
does not return inside the deadline's own close-out — is a deadline
propagation and close-out question of the run (REQ-exec-cancellation's
banked return; the post-commit render bound) and has no filing here.
The shape to reproduce: a `--changed` run with a derived oracle spanning
CLI tests under a short `--timeout`, observed past the deadline.
