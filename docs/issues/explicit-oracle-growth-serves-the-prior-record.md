# An explicit oracle list naming new tests is served from a record that never ran them

Field report from the protodb session (2026-10-02, the standing
channel; its tracking doc is protodb's
docs/issues/2026-10-02-gomutant-derived-oracle-closure.md).

After writing new tests for a record's survivors, a run whose targets
document names those tests beside the prior oracle (the same body, the
same prior oracle list) judges the record current and serves it as it
stands: the survivors stay open in the document though a test now
kills them. The only lever is `--force`, which re-measures every
candidate of every target in the document — 84 mutants over 28 targets
to flip ~20 survivors.

The derived-oracle route already has the rule the explicit route lacks:
REQ-result-stale's grown-set arm re-measures every recorded survivor
when the derived set grew ("an added test may kill it"; kills keyed to
unmoved oracles stand). A record served for a request whose explicit
oracle set is a strict superset of the record's is a stale survivor
verdict presented as current — the record's claim ("survived oracle X")
is true, the request's ("survived X ∪ Y") was never measured. The
derivation protodb proposes is the derived route's own: a record is
reusable as it stands only when its oracle set covers the requested
one; a request naming tests outside the record's oracle re-measures the
OPEN survivors against the added tests (a kill commits, a survival keeps
the record with its oracle list grown), kills stand — the same shape as
the grown-set arm, with the explicit list as the set.

Triage (304.1, 2026-10-03): the SERVE claim does not hold at HEAD — the
serve gate (freshness.go evidenceSetMatchesContextWithCurrent) refuses a
request whose oracle count differs from the record's and requires every
current oracle to hold a recorded row, and the killer-drift arm refuses a
grown set under an explicit oracle on either side; both are pinned
(run_test.go "an explicit request rode the derived-growth composition",
killerdrift_test.go "grown set under an explicit oracle"). An explicit
superset therefore re-measures WHOLE today, never serves; what the report
observed cannot be reproduced from it (a targets document whose
oracleExplicit list did not grow would serve, the new tests then being
outside the request). The cost ask stands and derives: with every pin
holding and the request's explicit set a superset of the record's, only
the open survivors need to run, and only against the ADDED tests — each
unmoved oracle's recorded pass stands exactly as a standing kill does,
the keystone REQ-result-stale already rests on; the compartment-ledger
"added = a new declaration" rule is not needed because there is no delta
— the added tests are simply unmeasured against the survivors. Kills
stand; a kill by an added test commits; a survival keeps the record with
its oracle list grown. REQ-result-stale's sentence "a grown set serves
only when the finding and the request are both non-explicit" gives a
scoping reason, not a soundness one, and is amended with the arm.

Lands: cross-tool train chunk 306 (gomutant, directly after 304 and
gofresh 305 in the lane).
