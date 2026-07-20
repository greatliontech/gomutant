# discover and run disagree about --changed targeting

Lands: 10 of the active hot-loop-ux plan (output and remediation audit),
or earlier if targeting is touched.

## Observed (2026-07-20, gofresh corpus)

Same tree, same flag: `gomutant discover --changed HEAD~2` listed seven
target symbols; `gomutant run --changed HEAD~2` measured ten. The three
discover omitted were the two newly added functions
(runtimeinput.WithBuildCacheRoot, runtimeinput.WithEphemeralTempRoot)
and the large host function whose body the change set edited in place
(runtimeinput.FromTestLogEnv, 471 candidates - the bulk of the
campaign). An agent sizing a run from discover's answer under-estimates
by an order of magnitude, and new code - the change set's most
important mutants - looks untargeted.

## Also

discover's default output is a full per-symbol oracle dump (63KB for
seven symbols) with no counts anywhere; the first questions - how many
symbols, how many candidates - are unanswerable from it. run's
`measure <symbol> N candidates` lines are the right shape; discover
should lead with them.
