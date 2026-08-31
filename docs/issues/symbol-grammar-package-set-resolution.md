# symbolPackage's dotted-path ambiguity has one verdict-bearing edge

symbolPackage's first-dot-after-last-slash cut is ambiguous for a
dotted non-vN package path element (documented at the function). The
one verdict-bearing consequence found (chunk-113 review): two dotted
SIBLING package dirs — a target in `…/pkg.beta`, an oracle in
`…/pkg.gamma` — both truncate to `…/pkg`, so packageProcessAttestable
compares equal and grants WithPackageProcessExecution's honesty
condition for processes that are not the target package's test
binary: a false positive in the evidence class, not a cosmetic merge.
Corpus-absent today (no consumed repo carries a dotted non-vN package
element; verified by module-path scan at filing).

Fix shape: resolve the cut against the LOADED PACKAGE SET instead of
guessing from the string — the tree knows its package paths, so the
cutter can pick the longest known-package prefix and fall back to the
grammar only for unloaded symbols. Consolidates the ambiguity out of
every consumer (grouping, attestability) at once.

Lands: when a dotted non-vN package path element enters any consumed
corpus (the fleet sweep's module-path scan is the watcher), or with
the next change to packageProcessAttestable.
