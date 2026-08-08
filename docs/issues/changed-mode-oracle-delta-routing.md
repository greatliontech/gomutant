# Test-only deltas hide their stale findings behind the residue row

Field report (agent consumer): the disposition loop the tool itself
prescribes — survivor → sharpen a test — produces a delta that
`--changed` classifies entirely as residue ("tests are oracles, never
targets"), while the records whose oracles moved (two records, 17 open
survivors each in the field case) sit stale and unmeasured. The
consumer has to know to re-run scoped by symbol.

Fix shape: the residue row names the consequence and the action —
"oracle closure of N stale findings — re-measure with --symbol …" —
so the prescribed loop closes without tribal knowledge. The routing
itself stays as specified (tests are oracles, never targets); only the
signpost is missing.

Lands: cross-tool train chunk 30 (gomutant consumer-surface bounds and
visibility).
