# `attest` cannot replace or withdraw an attestation

`gomutant attest` refuses a second attestation of the same survivor
("already attested"), and no command withdraws one. The ephemeral
face has `--reattest` for its own record; the findings-document face
has no counterpart, so a recorded equivalence reason that turns out
to be too broad — the common case when a reviewer narrows the
argument after the fact — can only be corrected by editing the
document by hand or by moving the mutation domain so the row sheds.

Field case (gitfs, 2026-09-12): a survivor in `flockBounded` (the
EINTR retry deleted) was attested with "EINTR is unreachable in a Go
process"; review narrowed it to "unreachable in a binary where every
installed handler carries SA_RESTART, which holds here because no
linked C code installs one". The narrower reason belongs on the
record, and the only home it could take was the commit message.

Wanted: `attest --reattest` replacing the reason (the same guard the
ephemeral face applies), and a withdraw form that returns the
survivor to open with the withdrawal reason on record, so the
attestation history reads forward like the findings document does.

Lands: awaiting triage
