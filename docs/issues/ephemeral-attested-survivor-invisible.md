# An attested equivalence is invisible when the probe is re-run

`ephemeral --attest` records a judged equivalence in the committed
record (`.gomutant/ephemeral-attestations.json`), but a later run of
the same edit batch reports a bare `SURVIVED` with no mention of the
attestation: an attested survivor and an un-dispositioned one print
alike. A reviewer verifying a change set's probes cannot tell the two
apart without opening the record and matching `editDigest` by hand —
which is exactly the confusion that surfaced on 2026-09-04 in the
bldc compile-rules loop, where an attested probe sat beside a
genuinely uncovered arm and the two read the same.

Wanted: when a probe's edit digest matches a row of the attestation
record, the verdict line says so (`SURVIVED — attested <digest>
<date>: <reasoning>`), and the MCP result carries the row; a
`--reattest`/`--attest` on an already-attested digest is reported as
such rather than silently appending a second row.

Reproduction: attest any equivalent survivor with `ephemeral --attest
"…"`, then run the identical batch again without `--attest`.

Lands: user decision.
