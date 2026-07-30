# One canceled freshness proof aborts the whole campaign

Lands: when a failed or canceled freshness-proof oracle invocation retries or
skips its one target with a decision line, instead of exiting the run.

## Observed

A consuming corpus (`candosa/cerebro`) ran a delta campaign while a full
`go test -p 2 ./...` sweep executed against the same tree — a caller sequencing
error, but one the tool answered maximally: the freshness proof for a single
target (`computePeriodicWith`, oracle = the full package suite) reported
`context canceled` under the concurrent load, and the whole campaign exited 1
from its prepare phase with nothing committed. The identical command relaunched
on a quiet tree ran normally.

## Shape

Same failure class as the resolved changed-`func init()` discovery abort
(now excluded loudly per REQ-target-changed's init arm): a per-target
condition escalated into a campaign-level outage. A canceled or timed-out freshness proof is evidence
about one target's oracle under momentary load, not about the campaign; the
admissible answers are a bounded retry of that proof, or committing the target
as unverifiable with the cancellation reason on its decision line while the
rest of the campaign proceeds. Exit-the-world should be reserved for conditions
that genuinely invalidate every measurement (a moving tree detected by the
stale-pin design is the good example — and that one already aborts loudly and
correctly).
