# Detached records with attestations are a silent dead end

Field report (agent consumer): a record sits detached carrying 4
attestations for a symbol that no longer exists (renamed across a
refactor). Nothing says "this will never re-attach," and there is no
prune or retarget verb (stipulator has both), so heavy refactoring
accretes dead overlay records that findings lists forever.

Fix shape: a detached record whose symbol no target resolves to is
loudly labeled terminal in every findings view; a prune verb removes
resolved-dead records (attestation reasoning preserved in the removal
record — promote-then-delete, not silent drop); a retarget verb rewrites
symbol identity across a rename so surviving attestations follow their
mutants exactly as stipulator's retarget carries bindings. Attestation
carry across retarget must stay pinned to survivor identity — position
and operator — not symbol text alone.

Lands: cross-tool train chunk 31 (gomutant detached-record lifecycle).
