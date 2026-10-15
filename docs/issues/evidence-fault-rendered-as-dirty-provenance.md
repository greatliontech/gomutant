# An evidence-walk fault is stamped and rendered as dirty worktree provenance

Lands: tooling-redesign 5.4.

Field report (greatliontech/pb, gomutant
v0.57.11-0.20260919151557-ee9616fd4b7b, gofresh v0.102.0): a
campaign over `internal/archive.ExtractZip` on a clean tree
(`git status` empty, the scratch namespace declared and gitignored)
banks the target machine-local with `findings` rendering one
reason, "dirty worktree provenance", while the record's evidence
also carries "external directory input: /" on every subject and an
open-closure observation. The dirty flag comes from the evidence
walk (`run.go`, the two `f.Dirty = true` arms: an evidence subject
with no view, or an unreadable runtime-input manifest), which
fail-closes by stamping the finding dirty on a non-staged run and
returns no reason; the portable line then reads the flag as git
drift, and `findings` stops at that first clause.

Two facts are conflated: a worktree git can vouch for or not, and
an evidence manifest the walk could not read. On a clean tree the
rendered reason is false, the true fault (which subject, which
manifest) is not carried, and the operator is sent to `git status`.
Expected: the evidence fault stamps its own clause naming the
subject — as the staged run already reports it — and "dirty
worktree provenance" means exactly git-visible drift.

The current non-staged fail-closed provenance path still uses `Dirty` for an
evidence-walk refusal. Separate producing git provenance from evidence validity
at the target/window boundary, without weakening either refusal. Completed
process unions intentionally ride all subjects; that sharing alone is not the
fault. Record-wide unavailable evidence and its diagnostic must not masquerade
as each subject's own observation. See
[divergence-reason-stamped-on-every-evidence-subject.md](divergence-reason-stamped-on-every-evidence-subject.md).
