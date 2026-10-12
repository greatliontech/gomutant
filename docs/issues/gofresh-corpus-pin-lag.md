# gofresh corpus pin lags the latest release (fleet sweep 2026-10-12)

The weekly fleet sweep's shape-corpus check reports this module pinned
to gofresh v0.115.1 while the latest release tag is v0.116.1 (the
remote is the authority; the local clone may lag it). Version lag is
the one drift channel the shape corpus leaves open — corpus content
drift is unrepresentable — so a consumer behind the release judges
purity, inputs and freshness with an older corpus than the one
released. The remedy is the bump: `go get
github.com/greatliontech/gofresh@v0.116.1`, whatever adoption riders
the release carries, tests green, then the binary reinstalled (the
provenance doc's own condition). The sweep's next corpus line reading
`current` is the check that closes this doc.

Scanned: the index and all 53 docs it lists. None schedules a gofresh
bump: the 2026-09-28 pin-lag doc closed at the v0.111.x bump and is
gone; knob-terse-clause-per-face and purity-assertion-run-surface
reference past bumps' readers only. Rows disjoint; no scheduled
trigger duplicated.

Lands: tooling-redesign 5.4 — gomutant's next chunk in the active
sequence (the migration of its target/window unit onto the released
evidence boundary; the train's rule is a producer's release before
the consumer bump that reads it). An earlier bump closes it sooner.
