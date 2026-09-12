# The caller's whole-run purity assertion has no run surface

gofresh accepts a purity assertion as "a caller's global assertion …
for a whole-run override" beside the per-symbol `//gofresh:pure`
directive (gofresh purity.md, REQ-purity-directive). gomutant's run
surface exposes `--vouch` for dependency variables but no way to pass
the caller's assertion, so a consumer facing an inferred downgrade
must annotate subjects one by one.

Field report (gitfs, v0.57.7): `internal/gc.lfsDigest` — which reads a
blob through go-git's EncodedObject interface and touches no network
— was downgraded on "startup effect: reaches net.Dial (network I/O)".
The reach is gofresh's interface-invoke over-approximation (every
implementation of the invoked method name is a target; some go-git
implementations dial), tracked in gofresh as
invoke-targets-narrowed-by-operand. Until that lands, the consumer's
only discharge is a directive on each affected declaration, and the
downgrade reasons name symbols the consumer never calls.

Asked: (1) a run-level purity assertion on both faces, recorded on
the evidence exactly as vouches are; (2) the downgrade reason to name
the invoke site in the subject (the call expression and its operand
type) beside the reached sink, so the consumer can tell an
over-approximated interface invoke from a real reach.

Lands: awaiting triage
