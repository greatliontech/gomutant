# The tree root's coordinate: one spelling at every root-side site

## What

Chunk 246's B3 gave the tree root one coordinate — `rootCoordinate(dir)`
(gotool.CanonicalDir, else filepath.Abs, else the spelling given) read
by the load and both guards, and `CheckTreeRoot` fired by `OpenStore`.
Root-side sites still resolve paths with their own `filepath.Abs` /
`filepath.EvalSymlinks` pairs: run.go (the second base of the
two-base check at the ephemeral edit's placement, checked first),
ephemeral.go, ephemeralattest.go, editbatch.go, provenance.go, and
store.go's own resolution wrap. Each answers the same question — "is
this path inside the tree, and which spelling is the record's?" — with
its own composition.

## Collapse sketch

One `treeRelative`-style helper over the load's coordinate answers
containment and the recorded spelling; each site reads it and drops its
pair. Invariants preserved: a record's spelling is the tree's canonical
relative path (REQ-exec-tree-root); containment is judged after
symlink resolution on both operands; a path outside the tree refuses
with the site's own message. Never a deletion of a refusal — every
site's refusal stays, only its coordinate composition moves.

## Why not now

The sites sit in five files across the root and the edit batch; the
fold changes recorded spellings for a symlinked tree only where a site
today resolves fewer links than the load — a behaviour move that needs
its own witness set (a linked tree per site), beyond 246's bump
coherence.

Lands: cross-tool train chunk 220 (gomutant's named smalls).
