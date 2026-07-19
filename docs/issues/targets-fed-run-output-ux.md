# Targets-fed runs: duplicate skip lines, and type-symbol skips lack a hint

Lands: 10 of the active hot-loop-ux plan (output and remediation audit).

## Observed

Feeding a stipulator binding-surface export whose implements symbols
are types (an interface and a struct — common for requirement-level
implements bindings), the run correctly skips them, but the output has
two rough edges. Each skip line prints twice ("skipped X (not a
function)" appears once per phase, apparently). And the skip reason
stops at "not a function" — correct, but the caller in the
stipulator-feed flow is left without the methodology step: the
requirement's enforcement surface exists, it just isn't
mutation-testable at the granularity bound. Live instance: tugboat's
REQ-node-core-custody/core-entropy surfaces (RawNode, EntropySource)
— two targets, four skip lines, zero mutants, and the useful next
action (bind methods or functions where mutation adequacy is wanted)
had to be inferred.

## Resolution

Deduplicate the skip reporting, and for type symbols add one hint
clause: "not a function — for mutation adequacy, target its methods
or the bound test's function-level subjects". A summary line
distinguishing "skipped: type symbols" from other skip classes would
also let the stipulator-feed flow report cleanly which requirements
had no mutable surface at all.
