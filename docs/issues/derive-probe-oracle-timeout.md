# Ephemeral probe oracle-timeout is a fixed knob where the cost is measurable

Field friction (chunk-112 review, 2026-08-29): reviewer probes
against the closure-class oracles burned attempts on baseline
timeouts — the fixed default (60s core, 10m as commonly passed)
neither reflects the oracle's actual cost nor the machine's current
contention, so a probe on a loaded host dies at baseline while the
same probe idles through most of its budget on a quiet one.

The automation-over-configuration directive shapes the fix: the
baseline run IS a measurement of the oracle's cost. Derive the
mutant-run budget from the observed baseline (a multiple with a
floor), and let an explicit oracle-timeout remain only as the
override for callers who genuinely know better. Campaign runs carry
the same derivable signal per oracle set; chunk 113's scoped-oracle
work (campaign-baseline-needs-scoped-oracles) is the natural seam —
the scoping that shrinks the oracle also measures it.

Lands: with train chunk 113 (gomutant ergonomics batch)
