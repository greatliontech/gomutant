# The ephemeral-attestation record has no lifecycle surface

The committed ephemeral-equivalence record
(REQ-result-ephemeral-attest) accumulates rows with no retirement
mechanism: prune and retarget do not know the record, and nothing
compares a stored edit digest to current content, so a row whose code
moved on persists with the commit+dirty stamp as its only staleness
signal (honest but passive — a reader must check the stamp against
the tree). Wanted: prune's resolved/dangling walks extend to
attestation rows whose files vanished or whose recorded provenance is
unreachable, and retarget follows file renames through the record —
the same lifecycle discipline the findings document already has.

Appended 2026-09-04 (bldc compile-rules loop): a row taken during an
adversarial loop — before the change set's commit, the normal case —
carries `commit: <HEAD>, dirty: true` where HEAD does not contain the
mutated file at all (the file was new in the change set), so the
provenance names a tree in which the path does not exist; `dirty`
discloses the state without making the row resolvable. Wanted with
the lifecycle: a row stamped pre-commit is re-anchored to the commit
that first carries the file at the recorded digest (or the record
carries the edit's content hash of the file as the anchor, commit
optional), so prune/retarget have something to walk.

Lands: with the next change to prune or retarget's record surfaces.
