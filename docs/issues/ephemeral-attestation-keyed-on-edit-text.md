# An ephemeral attestation is keyed on the edit's exact text

`Lands: user decision`

A consumer report (bldc, 2026-09-05). An `ephemeral` survivor attested
with `--attest` is recorded under the digest of its batch's edits. A
reviewer re-probing the same mutation with an independently spelled
edit — the same two-element slice swapped, the same guard dropped with
different surrounding context — gets a bare `SURVIVED`: the record
matches nothing, and the attestation guards exactly one spelling of
one mutant. Beside the earlier report that an attested survivor prints
as open even on the identical edit
(`ephemeral-attested-survivor-invisible`), this leaves a reviewer to
open the record and match by reading.

A firmer key would be the mutant's effect rather than its text: the
mutated file's post-edit content hash (two spellings of one edit
yield one file), or the AST of the replaced region. The consumer's
workaround is to state the attestation's reasoning in the change set's
record and to probe with the recorded batch file when re-auditing.
