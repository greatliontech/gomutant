# Shaped candidates derive twice per finding — thread the resolve-time derivation through

`shapedCandidates` runs at resolve (run.go, stored on the work item)
and again per finding at stamp (`shapedProvenanceFiles`) and at every
serve inspection. Under REQ-exec-quiescence the derivations must
agree, so the repeat buys no drift detection the stamp can act on —
for interface-satisfaction it is a full loaded-package AST walk plus
file reads per stamp, and the import-boundary linkage fold now adds a
type-graph walk with content reads. The linkage walk itself runs a
THIRD time: `shapedProvenanceFiles` calls `ForbiddenLinkageClosure`
again after `shapedCandidates` already did inside the same stamp. Threading the already-derived
candidates (and linkage) from resolve through the assembly arms to
the stamp collapses the mechanism to one derivation per run and
deletes the stamp-time derivation-failure arm outright (its staged
carve-out included). The serve-side re-derivation stays — it IS the
pin compare.

Lands: cross-tool train chunk 128 (serve carve-out consolidation —
same subsystem, same threading shapes).
