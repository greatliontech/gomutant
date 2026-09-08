# Tagged build legs and subprocess-built targets are outside campaign reach

A consumer with a js/wasm build leg (wisp: the boundary's tagged
half — the crossings, the drain decode, the capability wiring — and
every other js/wasm-only file) cannot measure that leg in either
direction. The host selection refuses the files as
build-constraint-excluded; a js/wasm selection discovers them but
has no oracle able to run — the consumer's proof suite is
browser-tagged and driven through Chromium, invisible to the
runner. The adversarial loop's hand probes cover the leg per change
set (edit, regenerate, run the browser test, restore), but a full
campaign records zero coverage of that half silently — the
harness-level cap a consumer's testing rules require surfacing, and
one the findings document cannot express today.

The same blindness covers out-of-process oracles: a target pinned
by a test that runs the target as a subprocess (`go run
./tools/platgen -check`, which compiles the on-disk source rather
than the campaign's mutated overlay) records as wholly
never-executed even though a real break fails the pin — the
mutation is never where the subprocess build can see it.

Two closing shapes, either sufficient for the first class and the
second needed for both:

- An external-oracle mode: a campaign delegates a selection's
  targets to a named oracle command (`--oracle-cmd`, per tag set or
  per package) that the runner invokes with the mutated tree
  materialized, reading a pass/fail verdict — the browser suite
  driven through Chromium becomes a first-class oracle, and the
  tagged leg measures like any other.
- Materialized mutation: an oracle mode that writes the mutant into
  a tree a subprocess build compiles (a worktree overlay or a
  `-overlay` file the harness threads to nested `go` invocations),
  so a subprocess-oracled main() measures against the mutant, not
  the disk.

The interim landed (cross-tool train chunk 163, 2026-09-08): a run under
a declared tag selection states the targets the leg discovers but no
oracle reaches as a coverage bound on both faces, and the findings
document carries the bound per selection (REQ-result-unreached-bound),
so the excluded leg never reads as zero silently. What remains here is
the measurement itself — one of the two shapes above.

Field report: wisp, 2026-09-05 (docs/issues/wasm-oracle-campaign-gap.md
there), on gomutant v0.52.0.

Lands: user decision — the external-oracle mode (or materialized mutation), deferred 2026-09-07: revisit when a consumer needs the tagged leg measured rather than declared; the stated-coverage-bound interim landed at cross-tool train chunk 163