# Served records never re-stamp provenance, stranding dev-loop attestations in the machine-local overlay

## Defect

A finding measured on a dirty worktree records `Dirty: true` and lives in the
machine-local overlay (`REQ-result-layers`). When the developer then commits
that exact content and runs again on the clean tree, the freshness proof holds
and the record **serves** — but the serve path (`spliceServedFinding` →
`commitAndAttribute`, run.go) carries the recorded provenance verbatim. The
growth and drift serve paths re-stamp provenance from the current tree
(`stampProvenance`, run.go:2228 — "recomputed like a fresh measure's"); the
pure serve path never does.

Consequence: a record born dirty stays `Dirty: true` forever while its serve
keeps validating, so `Committable` (store.go:87) keeps refusing it and it never
migrates to the repo document `.gomutant/findings.json` — **and its survivor
attestations migrate nowhere either**. The overlay is spec-defined as "a cache,
not a record", so the attestation's reasoning-on-record lives in a discardable
per-machine cache indefinitely.

## Demonstrated fault

The normal dev loop is exactly the trigger: measure and attest **before**
committing (mutation-testing the change set pre-commit is the intended
workflow).

1. Dev measures on the dirty tree → record (Dirty) → overlay.
2. `gomutant attest …` → attestation appended → record still Dirty → overlay.
3. Dev commits the identical content; every later `gomutant run` on clean HEAD
   serves the record (evidence is content-based, provenance is not a freshness
   pin) → provenance never recomputed → record never becomes committable.
4. A teammate's clone or a CI container has an empty overlay and no repo row:
   it re-measures from scratch and re-surfaces the attested-equivalent mutants
   as **open survivors** — the disposition and its reasoning are invisible off
   the author's machine, permanently.

Wrong result: an equivalence judgment made with reasoning on record
(`REQ-attest-survivor`) silently fails to reach any other machine even though
the current tree state fully satisfies the repo layer's portability conditions.

## Root cause

Provenance is stamped only at measure/growth/drift time and treated as
immutable on a pure serve, while committability is judged from that frozen
provenance rather than from the tree the serve just revalidated the evidence
against. The layer-split invariant ("repo carries only evidence a reviewer on
another machine can inherit soundly") would not be violated by promotion: a
serve's freshness proof revalidates every evidence record against the current
tree, so when HEAD is clean and the proof holds, the served record is exactly
as inheritable as a fresh clean-tree measure.

## Fix shape

On a successful pure serve, re-stamp provenance from the current tree exactly
as the growth and drift paths do; `Store.Update`'s committability split then
promotes the record to the repo document and deletes the shadowing overlay
entry (both mechanisms already exist — "an overlay entry is deleted the moment
its symbol gains a committable record"). Spec: `REQ-result-layers` (or a
serve-path clause in `REQ-result-stale`) gains the sentence that a serve
recomputes provenance like a fresh measure. Attestation carry needs no change —
`sameAttestationPins` already excludes provenance.

Test shape: measure + attest on a dirty tree; commit identical content; run
again; assert the repo document now carries the record with its attestation and
the overlay entry is gone; assert a fresh store on a simulated second machine
(empty overlay) serves the attested finding.

Lands: cross-tool train chunk 26 (gomutant serve-path provenance re-stamp).
