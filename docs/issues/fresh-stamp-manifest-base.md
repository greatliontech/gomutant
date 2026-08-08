# Fresh-measure dirtiness resolves observation manifests against the git toplevel, not the module

`pathsDirtyContext` materializes the run observation's manifest paths
against the repository root. Manifest identities are module-relative,
recorded and validated against the subject's module directory — the
serve-path stamp resolves per-subject bases for exactly this reason.
When the module sits below the repository root (monorepo, workspace
member), a fresh measure's runtime-input paths materialize at
`root/<rel>` instead of `<moduleDir>/<rel>`: local-but-nonexistent,
git status reports nothing, and a runtime input's dirtiness is
invisible to the fresh stamp — a false-clean record can reach the repo
document claiming provenance its inputs don't have. Every consumer
revalidates evidence before serving, so the cost is a
contract-violating portable row and a re-measure on other machines,
never a wrong verdict.

Same family: `Committable` resolves every subject's manifest against
the one store module directory; a workspace oracle living in another
member module resolves against the wrong base there too (both
directions of wrongness are possible: paths escaping the module refuse
committability, paths silently mislocated pass it).

Fix shape: thread per-subject module directories into the fresh stamp's
dirtiness computation (the observation knows its module) and into
`Committable`'s path enumeration (evidence would need to carry or
derive its subject's module-relative base).

Lands: cross-tool train chunk 31.
