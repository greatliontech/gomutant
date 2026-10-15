# A campaign over gomutant's own root package pays a day of oracle time

Lands: tooling-redesign 9.3.

The historical close-out campaign (`gomutant run --changed 9753a5e`)
selected 46 targets: 35 in the root package, 5 in `internal/engine`, 4
in `internal/mcpserver`, 2 in `internal/cmd`. The root package's test
binary took 22 to 25 minutes on the shared machine: 539 tests, many of
them real campaigns over fixture modules that spawn `go test`. Every
root-package target inherited that binary as its derived oracle, so its
baseline alone exceeded ten minutes and each mutant ran the slow
integration tests that cover it; at budget 12 the estimate was
many hours and plausibly more than a day.

The reported oracle tests also executed `/usr/bin/git`, outside the declared
observation bracket. Declaring `--bracket-path /usr/bin/git` pins that dependency
by content; it does not by itself establish every other placement or reuse pin.

What happened: a first pass with an explicit `--oracle-timeout 10m`
skipped 40 targets (the timeout also bounds the baseline probe, which
the root package exceeds) and committed three, all machine-local
(16 killed, 9 open); a rerun under a 45-minute timeout was stopped by
the session for low memory before it reported.

The later parked prefix (42/89 banked, 457 killed, 443 open) is historical and
non-authoritative: its bank/records precede the repaired admission and execution
policy. Recover the original selected population and complete deciding oracles
from the original tree and invocation, reconciling the 46-target report with
that later attempt rather than treating a new changed-scope query as identical.

Current full-oracle@3 requires each required group unsplit. Do not substitute
short/default oracles, exclude integration tests, or choose a cheaper explicit
inventory to claim completion of the original scope. Any harness optimization
must preserve that population and deciding assertions. A Git bracket covers that
dependency only; it does not guarantee that every record is portable or reusable.

Secondary constituents: tooling-redesign 6.6 makes selections coexist; 6.7
provides bounded recovery before final long campaigns; 7.7 measures comparable
cost. This issue records retained work, not authorization to execute a campaign.
