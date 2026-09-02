# A changed-ref campaign reports whole-symbol survivors, burying the delta's own signal

`run --changed=<ref>` targets every symbol whose body differs from
the ref, then measures the WHOLE symbol. On a large function that
gained a few dozen lines (a classifier of ~1800 candidates gaining
one arm) the summary reports hundreds of open survivors of which a
handful sit on lines the change introduced — the number a chunk
close-out's delta judgment actually needs. Reconstructing it today
means dumping `findings --json --detail`, intersecting each open
survivor's position with the diff's added-line hunks, and
restricting to the symbols the run measured, because the document
merges every layer's records — including local-layer records from
earlier campaigns whose positions predate the current tree — under
one symbol key with no run identity to filter on.

Two surfaces would close it: a changed-lines filter on the run's
summary and result rows (survivors on the delta's added lines
counted and listed distinctly from the symbol's pre-existing
remainder), and a run identity on findings rows so an inspection
can scope to one campaign's records without re-deriving the
measured set from the log.

Lands: cross-tool train chunk 139 (gofresh docs/plans/cross-tool-train.md; the run-identity/filter split is decided at its triage gate).
