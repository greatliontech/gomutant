# findings inspection re-judges the store and the document bloats

Field reports (pando agent, 2026-08-22, twice in one day): on a
28 MB findings document (~85 targets), `gomutant findings` —
documented as "inspects without running anything" — took over five
minutes: state classification re-derives freshness per record (the
same cold dynamic-state derivation cost pew stat pays), and nothing
tells the caller a campaign lock or a long judgment is in progress
(the lock-wait notice is the stale-marker issue's ask; this issue is
the judgment cost). Also 28 MB for 85 targets wants an audit:
candidate evidence rows dominate, and an inspection-grade summary
should not need them paged in at all.

Asks: an inspection mode that reads recorded state without
re-judging freshness (state-as-recorded, with a --judge opt-in for
the expensive truth); a document layout or index that keeps summary
reads O(targets) not O(evidence); the existing --symbol/--state
filters documented as the cheap path.

Lands: user decision.
