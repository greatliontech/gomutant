# Unverifiable refusals should name their discharge channel

Field report (pando agent, 2026-08-22): four targets read
"package graph shares mutated dynamic state: github.com/zeebo/xxh3:
xxh3.key escapes writable" and the agent asked for a new
"--assume-immutable" flag — not knowing --vouch IMPORT:VAR already
exists and that xxh3:key is a standing audited vouch in sibling
repos. The refusal is correct; its message is a dead end. Each
unverifiable reason should name the applicable channel: a
version-pinned dependency variable names the vouch flag and its
audit obligation; a mutable-local variable names the
//gofresh:single-subject directive (with its attestation caveat) or
the in-code restructure; the shared engine's other discharge classes
name theirs. Same message surface in stipulator and pew (the shared
gofresh reason strings), so the fix belongs at the reason-rendering
seam, once.

Lands: user decision.
