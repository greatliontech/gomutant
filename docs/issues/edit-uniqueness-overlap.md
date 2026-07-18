# Exact-match edit uniqueness ignores overlapping occurrences

Lands: when exact-match edit uniqueness counts overlapping match starts, so a
self-overlapping pattern with more than one valid start is refused as
ambiguous.

## Observed

Both edit paths count occurrences non-overlappingly: `ApplyEditsContext` uses
`strings.Count` (`ephemeral.go`, the switch over `n`) and
`prepareEditBatchContext` uses `bytes.Count` (`editbatch.go`, the switch over
`count`). In content `aaa`, the pattern `aa` has valid match starts at offsets
0 and 1, but the non-overlapping count is 1, so the edit is accepted as unique
and applied at offset 0 — a guessed location. REQ-exec-ephemeral
(`docs/specs/execution.md`) requires an edit that "matches more than once" to
be refused rather than guessed, and the batch contract requires the old string
to occur "exactly once in that file's original bytes"; a self-overlapping
pattern with two match starts satisfies neither reading, yet is applied.

This is distinct from the specified sequential-edit semantics: the defect is
in what counts as "once", not in edit ordering.

## Resolution

Count match starts (for example, scan with `strings.Index` from each match
position + 1) in both the sequential and batch paths, and refuse a pattern
with more than one start as ambiguous, with a regression case for a
self-overlapping pattern.
