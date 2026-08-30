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

Lands: with the next change to prune or retarget's record surfaces.
