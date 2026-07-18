# Concurrent mutant runs share runtime resources

Lands: when mutant oracle execution isolates concurrent mutant test processes'
shared runtime resources, or interference-induced oracle failures are
differentially distinguished from mutant-caused kills.

## Observed

Build overlays isolate compilation, not runtime. Every mutant oracle process is
a `go test` invocation running in the real tree directory with only an overlay
file distinguishing it (`internal/engine/run.go`, `runMutantOnce`), and the
worker pool runs up to `Jobs` mutants concurrently (`run.go`, phase-two pool;
the `Options.Jobs` comment acknowledges "load-induced flakes read as kills, so
the default hedges").

Concurrent oracle test binaries therefore share the working directory and
package directories, network ports, fixed temporary names, inherited
configuration, and external system resources. When two mutants' oracle
processes collide on such a resource, the losing process reports a named
test-level failure, which is an attributed kill under REQ-exec-attribution —
a false kill caused by a sibling mutant, not by the measured mutant. The
runtime-input observation boundary (REQ-exec-observation) captures what a
process read; it cannot detect that a concurrent sibling perturbed a shared
resource, so the corrupted measurement is not refused.

The differential baseline probe clears only package-scope failures; a
test-level failure from interference is scored directly.

## Resolution

Either give each mutant process an isolated runtime view of the shared
resources oracle tests commonly touch (working directory, temp namespace) and
document the residual shared-resource classes, or re-run a killed mutant's
failing oracle serially before scoring the kill so a collision-induced failure
is distinguished from a reproducible mutant-caused one.
