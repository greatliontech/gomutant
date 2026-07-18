# Ephemeral runs accept unexercised replacements and unvalidated test packages

Lands: when ephemeral runs refuse replacement files absent from the selected
build and refuse test-package arguments that are not loaded package import
paths.

## Observed

Two validation gaps share the ephemeral entry seam.

Unexercised overlay reads as a survivor. `resolveTreeFile`
(`editbatch.go:172-200`) confines a replacement to an in-tree regular file, and
`Ephemeral` rejects a byte-identical replacement (`ephemeral.go:50-62`), but
nothing requires the file to participate in the selected build. Overlaying a
build-constraint-excluded `.go` file, or a data file the compiler never reads
(overlays affect the build, not runtime file access), produces a run in which
the mutation is never exercised: the baseline probe passes, every oracle test
passes, and the result reports `killed: false` — a false "the tests did not
notice" verdict for a mutant that was never present (REQ-exec-ephemeral's
attribution intent).

Test package in an option position. The caller-supplied `testPkg` is passed
into `go test` argument lists without validation (`ephemeral.go`, `runEphemeral`
→ `internal/engine/run.go`, `goTestArgs` call sites); the MCP face checks only
non-emptiness (`internal/mcpserver/server.go`, ephemeral input check). A value
beginning with `-` (for example `-exec=...`) parses as a `go test` flag rather
than a package path, silently changing the invocation being measured.

## Resolution

Validate `testPkg` against the loaded packages (as run targeting already does
via package resolution) before any process launches, and refuse a replacement
whose file is not among the loaded build's compiled files for the probed test
package, naming the exclusion instead of scoring an unexercised mutant.
