# Progress output has no summary cadence and no exit-path report

Field report (pando agent, 2026-08-22, ~85 targets / 4.6k candidates):
per-confirmation lines dominate the stream (one line per serial
confirmation) while the informative line (executing target N/M,
cumulative candidates) is rare; a budget exit (--timeout) ends with
"context deadline exceeded" and no banked-state summary; a signal
exit likewise; "analysis observe" lines are unexplained vocabulary;
and a resumed request reports a different target denominator (7/71
vs 20/85 for the same ref — the count is targets-to-measure after
serving, but nothing says "71 remaining of 85"). The confirmation
pass does not name its mode (sampled stride vs per-kill full
confirmation — the disarmed-stride state looks identical to the
armed one from the log).

Asks, one issue because they are one surface: a compact summary line
at a fixed cadence (targets done/total, candidates measured, kills,
elapsed); a banked-state summary on EVERY exit path (normal, budget,
signal, abort); denominators as remaining-of-total; the confirmation
mode named once per target; the analysis phase lines speaking the
findings-doc vocabulary; JSON-lines structured output as an option.

Lands: user decision.
