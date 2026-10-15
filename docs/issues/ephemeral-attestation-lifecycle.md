# The ephemeral-attestation record has no lifecycle surface

The committed ephemeral-equivalence record
(REQ-result-ephemeral-attest) accumulates rows with no retirement
mechanism: prune and retarget do not know the record, and nothing
performs a bulk current-content lifecycle judgment. Probe-time digest matching
does exist; it is not a prune/retarget surface. A row whose code moved on can
remain as history. Wanted: prune's resolved/dangling walks extend to
attestation rows whose files vanished or whose recorded provenance is
unreachable, and retarget follows file renames through the record —
the same lifecycle discipline the findings document already has.

Appended 2026-09-04 (bldc compile-rules loop): a row taken during an
adversarial loop — before the change set's commit, the normal case —
carries `commit: <HEAD>, dirty: true` where HEAD does not contain the
mutated file at all (the file was new in the change set), so the
provenance names a tree in which the path does not exist; `dirty`
discloses the state without making the row resolvable. The report proposed
re-anchoring the row to the later commit that contains matching bytes, or
retaining a content anchor without a commit. Rewriting producing provenance
is superseded by the authority contract. The remaining need is resolvable
file/content identity for lifecycle operations, not a fictitious later
producing commit.

Preserve original producing provenance rather than silently re-anchoring it to
a later commit. Any new applicability endpoint needs its own explicit license;
matching bytes or a rename cannot fabricate historical execution support.
Follow files, oracle identity and selections together, with check previews and
consistent behavior across both findings layers and the separate attestations.

Lands: tooling-redesign 6.6.
