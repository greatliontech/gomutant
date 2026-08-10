# Overlay-bypassed oracles: disk-reading tests report verdicts on mutants they never saw

Lands: cross-tool train chunk 39.

## Observed

Field report from the cerebro corpus. A class of tests derives its verdict
from re-reading source bytes off disk rather than from the linked build:
architecture-law scans that walk the tree from a repo-root ascent
(`os.ReadDir` + `os.ReadFile` + `go/parser` over the checkout's own files),
and law tests that spawn `go list` or other subprocesses which re-resolve
sources from the filesystem. The mutant runs through the compile-time build
overlay and never touches the tree — correct and load-bearing
(REQ-mut-overlay; the preparation pipelining's clean-tree probes beside
executing mutants rest on it) — so for these oracles the mutated content is
invisible by construction: the compiler links the mutant, but the
verdict-bearing reads see the unmutated tree.

The failure direction is the dangerous one: the oracle runs, every disk read
sees the original bytes, the tests pass, and the result reports a clean
`killed: false` verdict for a mutant the oracle could not have observed. In
the reporting corpus this covers the whole
import-graph/vocabulary/registration-sweep law family (the tests enforcing
the machinery frontier). It is a false-survivor channel exactly like
ephemeral-replacement-outside-oracle-closure, with a different mechanism:
there the mutated package is never linked; here it is linked and the
oracle's evidence path bypasses the overlay anyway. Ephemeral probes hit the
same wall — a probe against a disk-reading test always reports the mutant
unnoticed — so the reporting corpus's standing discipline for this family is
in-tree edits with verified restore, flagged each time as an exception to
the never-hand-edit rule the tool otherwise makes unnecessary.

## Resolution

A real fix would need tree mutation (breaks the overlay invariant and the
concurrent clean-tree-probe model) or a filesystem-level overlay (mount
namespaces/FUSE — heavy, platform-bound). But the runtime observation
apparatus already reports the exact reads that bypass the overlay — the
`reaches os.ReadFile (file I/O)` / `reaches os.ReadDir (file I/O)`
observations and the subprocess reaches visible in witness evidence today.

Mirror the sibling issue's labeling shape: cross-check the oracle's observed
filesystem reads against the mutated file's own tree path (and, for
subprocess reaches, against the mutated package's directory). When the
oracle's evidence path demonstrably re-read the mutated content's original
location from disk, the result is not a verdict — bucket it
`overlay-bypassed: the oracle read the mutated path from the tree`, the way
never-executed survivors are bucketed, so the false-survivor reading is
labeled instead of silent. An `explain` answer for such a record names the
bypassing read (path, observation kind) so the consumer knows which test to
restructure.

Consumer-side mitigation in use meanwhile (for the record): the reporting
corpus refactors disk-reading laws into a pure core over synthetic source
tables (overlay-mutable, probed normally) plus a thin disk-walking shell
(outside overlay reach, probed by in-tree edit with restore). Detection
upstream makes the remaining shell honest in findings documents instead of
silently unmeasurable.
