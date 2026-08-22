# Condition-negate generator emits unparseable code on inline interface type assertions

Filed from a protodb campaign (operator's standing queue instruction;
uncommitted for this repo's session to triage).

Observed on `gomutant run --changed <ref>` against
`github.com/greatliontech/protodb/internal/replication/consensus/tugboat.Host.Close`:

    gomutant: target ...Host.Close: generate ...Host.Close: format
    candidate host.go:223:5 condition: negate: 199:1: expected '}',
    found '!' (and 1 more errors)

The condition at the candidate site was an `ok`-guard whose init
statement carries an INLINE interface type in the assertion:

    if c, ok := h.tr.(interface{ Close() }); ok {

The negate candidate's printed form fails to re-parse — the inline
`interface{ Close() }` type literal inside the two-value type-assert
init appears to break the generator's condition rewrite (a `!` lands
where the printer expects the interface body's brace structure).
The campaign errors out at generate time rather than skipping the
candidate, so one unrepresentable candidate kills the whole run.

Three independent defects worth separating:
1. The rewrite/printing of negate candidates over conditions whose
   init statement contains an inline interface (or likely any
   composite type literal) in a type assertion.
2. Failure containment: a single malformed candidate should record as
   an infrastructure-skipped candidate on that target, not abort the
   campaign.
3. Abort residue: the aborted campaign left its
   `findings.json.campaign` pid marker behind (pid long dead), and a
   later `findings` MCP inspection blocked on the marker INDEFINITELY
   — 30 minutes of silence, no holder named, no pid-liveness check —
   while `run` apparently reaps or ignores the same stale marker.
   The observed cost: an inspection that "runs no tests" wedging a
   session until the marker is hand-removed. Reap stale-pid markers
   on every entry point, or at least name the holder in a prompt
   refusal.

Reproducer shape (any package):

    if c, ok := v.(interface{ Close() }); ok { c.Close() }

protodb has meanwhile named the interface (a consolidation on its own
merits), so its campaigns no longer trip this — the reproducer above
still does.

Lands: user decision.
