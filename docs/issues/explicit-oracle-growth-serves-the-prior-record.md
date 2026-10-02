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

Lands: cross-tool train chunk 304 (its open triages this beside the
derived-oracle report; the reuse posture's channel naming — "not
reusable as it stands; what lifts it: the new oracle" — is 183's
vocabulary and lands with the rule).
