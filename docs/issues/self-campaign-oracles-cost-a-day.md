# A campaign over gomutant's own root package pays a day of oracle time

Lands: cross-tool train chunk 284 (self-campaign oracles gomutant can
afford, then chunk 277's close-out campaign over the result)

Chunk 277's close-out campaign (`gomutant run --changed 9753a5e`)
selects 46 targets: 35 in the root package, 5 in `internal/engine`, 4
in `internal/mcpserver`, 2 in `internal/cmd`. The root package's test
binary takes 22 to 25 minutes on the shared machine: 539 tests, many of
them real campaigns over fixture modules that spawn `go test`. Every
root-package target inherits that binary as its derived oracle, so its
baseline alone exceeds ten minutes and each mutant runs the slow
integration tests that cover it; at budget 12 the campaign runs for
many hours and plausibly more than a day.

The oracle tests also execute `/usr/bin/git`, which lies outside the
observation bracket: every record the campaign commits is
machine-local unless the run declares `--bracket-path /usr/bin/git`,
under which git is pinned by its content and the records are portable.

What happened: a first pass with an explicit `--oracle-timeout 10m`
skipped 40 targets (the timeout also bounds the baseline probe, which
the root package exceeds) and committed three, all machine-local
(16 killed, 9 open); a rerun under a 45-minute timeout was stopped by
the session for low memory before it reported.

The chunk decides the layout under which a root-package target's
oracle is the tests that exercise it — the integration campaigns in a
package or build configuration of their own, or an explicit-oracle
targets document for self-campaigns — preserving every existing test
and its bindings, then runs chunk 277's close-out campaign
(`--changed 9753a5e`, the default derived oracle timeout,
`--bracket-path /usr/bin/git`) and dispositions its survivors.
