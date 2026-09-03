# An ephemeral probe's rapid failfile lands in the caller's tree

`Lands: user decision`

`ephemeral` promises the caller's tree is never touched: the mutant is
built and tested in a copy. A property test under pgregory.net/rapid
that fails under the mutant writes its failfile to
`testdata/rapid/<Test>/<stamp>.fail` beside the test's source — and
with the probe's build resolving the package directory to the real
tree (the copy shares the module's source paths through the overlay
or the copied file's original location), the failfile appears in the
caller's working tree. Observed 2026-09-03 in bldc (consumer report):
two `.fail` files after two probes of a rapid property in
`internal/compile/flow`; gitignored there, so no commit was polluted,
but a stale failfile is a replay seed the next honest run picks up,
and an un-ignored repository would see it as an untracked change.

Asks: run the probe's tests with rapid's failfile directory pointed
into the probe's temp dir (`-rapid.failfile`-class configuration, or
`TMPDIR`/working-directory isolation for `go test`), or at least
detect and remove files created under the caller's tree during a
probe and say so in the result.
