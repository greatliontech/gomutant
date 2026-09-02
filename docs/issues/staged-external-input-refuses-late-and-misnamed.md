# Staged mode refuses an external input at commit time, under a drift headline

Field report (2026-09-02): a `--staged` campaign measured for fifty
minutes and refused every target at its provenance stamp with
"unstaged drift over the measured package's inputs (measured input
outside the repository: <path>); stage or stash it to pin the
snapshot". The input was a replace module outside the repository —
read by the compiler, observed during each target's
"observing oracle runtime inputs" step, and never pinnable by the
index snapshot. Nothing drifted and nothing could be staged.

Two defects:

- **Late.** The refusal is derived in the provenance stamp, after a
  target's measurement completes. The observation that decides it
  exists before the first mutant runs; an input the staged snapshot
  can never vouch for should refuse at preparation, per target,
  before any oracle executes — the campaign's cost is then the
  planning pass, not the measurement.
- **Misnamed.** The headline is the unstaged-drift text with the
  remedy "stage or stash"; the true cause rides only in the
  parenthetical. The results spec already says an identity outside
  the repository is not git's to vouch for and does not stamp dirty
  (it keeps the record machine-local under the machine-local-input
  clause), so the staged arm's dirty judgment over an external
  identity diverges from the spec: the refusal must headline the
  external input and name its remedy (declare the surface as a
  bracket path, or measure unstaged), never a drift the tree does
  not carry.

Reachable path: `pathsDirtyContext` answers dirty with
"measured input outside the repository" for any path not local to
the root, and `stampProvenance`'s staged arm renders every dirty
judgment as unstaged drift.

Lands: cross-tool train chunk 156 (gofresh docs/plans/cross-tool-train.md; chunk 146 folded into it).
