# Repo and local mutation cache split

Lands: when clean reusable mutation evidence is written to a repo cache and
dirty, runtime-unverifiable, or machine-specific evidence is written to a local
cache that gomutant reads as an overlay.

## Observed

During a changed-scope run over a consuming repository's standalone shell work,
gomutant wrote `.gomutant/findings.json` containing useful results but also
records with dirty provenance, `/tmp` runtime observations, PTY-related
unverifiable state, and attested equivalents from the uncommitted worktree.
Those records were valuable for the current developer because they avoided
remeasuring already-covered candidates, but they were not a portable repo
artifact a reviewer should inherit.

Keeping the file entirely local loses the main hotpath value: full mutation
sweeps are expensive, and developers need to reuse every clean result that can
be safely shared. Committing the whole file, however, mixes clean reusable
evidence with local execution state and makes code review carry machine-specific
runtime facts.

## Resolution

Split mutation evidence into two layers. The repo layer stores only clean,
portable, content-pinned records whose reuse is justified by target/oracle source
closures, operator identity, toolchain/build settings, and stable runtime
evidence. A local cache, for example under the user's cache directory, stores
dirty worktree measurements, unverifiable runtime observations, partial runs,
and manual probes. A run reads both layers, prefers valid repo evidence when
available, overlays local evidence for the active checkout, writes newly clean
records to the repo layer, and writes local-only records to the local layer.

The split should be automatic in the default developer workflow. A user should
not have to choose between committing no cache and committing dirty local state.
