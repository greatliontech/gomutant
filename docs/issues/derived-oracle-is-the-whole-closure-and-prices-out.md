# The derived oracle is the whole linking closure, and a real tree cannot commit a target in an hour

Field report from the protodb session (2026-10-02, the standing
channel; its own tracking doc is protodb's
docs/issues/2026-10-02-gomutant-derived-oracle-closure.md).

`gomutant run --changed b819d832 --staged --budget 3 --jobs 8
--analysis-budget 5s --timeout 75m` over protodb (HEAD 3377b023 with a
staged close-out set) selected 112 targets; every `measure` line read
"oracle: 2269 tests across 10 packages" — the root package and
internal/db link everything, and internal/db's plain suite alone is
1,560 tests, each opening a store — and after 75 minutes "0/112
targets committed, candidates 17/161, 0 killed, 0 open, est ~8h
remaining", then "banked command timeout … 0 target(s) committed". The
previous chunk's close-out wedged the same way for over two hours.
The workaround in use is a targets document with `oracleExplicit` per
symbol (the symbol's own package tests; internal/db symbols narrowed
by name stem) built by a script from `gomutant discover --json` and
`go test -list` — a per-campaign hand artifact that can drop an oracle
silently, carrying the derivation the tool is meant to own. The same
class stalled the train's own pb campaign (chunk 257's lesson) and
priced gomutant's self-campaign to a day (chunk 284's charter).

The derivation, from the execution spec's own rules: the derived
oracle is the runnable tests of every package whose test binary links
the target's package — sound and maximal, and over a tree whose root
links everything it is the whole suite for every target. Narrowing it
soundly is the rule gomutant already applies AFTER measurement to a
survivor (covering-passed: the covering tests passed, the non-reaching
remainder exempt on measured coverage) — a test whose execution never
reaches the mutated function cannot observe the mutation. Applying
that rule BEFORE measurement — the oracle of a target is the tests the
baseline's coverage shows reaching its package (per test, from the
baseline run the campaign already pays), the non-reaching remainder
exempt on that measured coverage — turns the 2,269-test oracle into
each target's reaching tests while keeping the kill and survivor
verdicts exactly as sound as the narrowed survivor is today. Of the
other two shapes the report names: ordering a window so the own-package
oracle runs first changes only the order (a survivor is a universal
claim over its oracle and cannot commit on a prefix; a kill already
commits at its first failing test), and committing a measured prefix
past a test-count bound would declare survivors over an oracle never
run — unsound, refused; the sound bound is the one chunks 114/137 set:
a target whose narrowed oracle still prices past the window is
REFUSED with its pricing recorded and filed for an idle window, never
silently truncated.

Lands: cross-tool train chunk 304 (gomutant: the derived oracle
narrowed by the baseline's measured reachability before measurement;
the pricing refusal past the window; the condition the report states —
a `--changed` run over protodb's tree commits its first target within
an hour with no targets document — is its measure at close).
