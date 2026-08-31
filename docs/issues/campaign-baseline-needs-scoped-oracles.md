# Suite-scale oracles: single-target campaigns landed, chunk-scale remains a ruled fork

Chunk 113's landing check ran with everything it chartered in place
(linkage-scoped oracle derivation, per-group derived budgets,
coverage-guided kill scheduling) and split the verdict:

- SINGLE-TARGET campaigns are now feasible, measured: the worst-case
  engine target (methodDeclarationRewrite, 127 candidates, oracle 587
  tests across 4 packages) completed in 12h43m — 58 killed, 28 open,
  41 discarded — where the pre-fix run recorded 0 kills against a
  noise oracle and whole-suite baselines timed out unconditionally.
  Kills are cheap (the schedule fronts probable killers); SURVIVORS
  dominate at the full linked-suite cost (~20 min each here).
- CHUNK-SCALE `--changed` campaigns remain infeasible: the chunk-113
  gate preflight (`--changed a577f0d --plan`) selects 81 targets /
  8,720 candidates, which the measured survivor cost extrapolates to
  weeks. The gofresh face carries the same shape (~6.5-min root
  suite). Chunk gates therefore still stand on `gomutant ephemeral`
  probes with named deciding tests.

The residual is a genuine contract fork neither first principles nor
the record settles alone:
(a) SUITE DECOMPOSITION — split the monolithic root suites so derived
    per-group oracles shrink; keeps every verdict's oracle semantics,
    costs test-architecture work in each tool repo;
(b) SURVIVOR-ORACLE NARROWING — score survivors against the
    coverage-derived covering-test subset instead of the full linked
    suite (kills already effectively do this via the schedule);
    collapses survivor cost from minutes to seconds but CHANGES WHAT A
    SURVIVOR MEANS (a non-covering test can no longer refute), the
    verdict-flipping narrowing class reserved for the user;
(c) ACCEPT day/week-class campaigns as idle-window background work
    only, with chunk gates permanently probe-based.

Lands: user decision (the fork above, raised with these measurements).
