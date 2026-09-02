# A relative bracket path resolves against the target's module, not the invocation

`run --help` describes `--bracket-path` as "module-relative path or
absolute file". In a go.work workspace the module that resolves it
is the TARGET's: invoking from the workspace root with
`--bracket-path internal/boundary/nontree_pump.go` (a root-module
file the nested tools module's tests read at run time) leaves the
nested module's targets planned as `unverifiable: runtime input not
covered by observation bracket: <root>/internal/boundary/nontree_pump.go`
— the declared surface and the observed read name the same file,
but the declaration was joined onto the nested module's directory
and never matched. Absolute paths do match, at a cost the consumer
records separately: absolute brackets mark every measured record
machine-local, so attestation dispositions never reach the
committed findings document and a clean checkout re-litigates every
survivor.

Reproduction (v0.52.0, workspace with a nested tools module whose
tests read a root-module file): `run --plan --staged --changed=HEAD
--bracket-path <root-relative file>` from the workspace root; the
nested module's targets plan unverifiable naming the absolute path
of that same file.

The fix is a resolution rule: resolve a relative bracket path
against the invocation's `--dir` (or the workspace root) first, and
treat the result as the declared surface for every module's
oracles; the machine-local disqualifier then never fires for an
in-tree file spelled relatively.

Lands: cross-tool train chunk 138 (gofresh docs/plans/cross-tool-train.md).
