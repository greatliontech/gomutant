# Semantic closure identity in the disposition carry gate

REQ-attest-survivor keys the disposition carry to the mutation domain
(body hash + operator set), deliberately excluding the closure. The
field failure that motivated the rule was a measurement-pin move over
byte-identical subjects: the dynamic-state strategy pin moved (gofresh
@33) while bodyHash, operator set, positions, and site content were all
unchanged, wiping 61 dispositions (49 of them silently, via the
aborted-run epilogue this change set also fixed). The closure was NOT
that event's mover — but the same campaign demonstrated the closure's
own over-triggering separately: a dependency doc-comment edit moved
four targets' recorded closures ("stale (target: closure)"), because
the content-hashed closure cannot distinguish a comment edit from a
semantic change. Putting the closure in the carry gate would therefore
shed dispositions on changes provably unable to bear on equivalence.

The accepted risk of excluding it is stated in the requirement: a
dependency-semantics move can flip an equivalence while a never-executed
mutant keeps surviving, carrying stale reasoning.

A middle gate would dominate both poles: shed when the closure moved
SEMANTICALLY (a change able to bear on the mutant's meaning), carry
across comment/format-only closure motion. That needs a closure identity
insensitive to non-semantic edits — gofresh's closure is already
reachability-scoped (an unreachable edit does not move it) but its hash
is content-based, so comments and formatting count. If gofresh grows a
canonicalized closure identity (the analogue of gomutant's canonical
body hash, over the whole closure), the carry gate adds it as a third
component and the stated risk clause narrows accordingly.

The 2026-08-21 review's derivation, for the record: closure-in-the-gate
as-is sheds on changes provably unable to bear on equivalence; body-only
(current) carries across changes that can; neither defeats the other on
soundness — re-execution plus the contradiction guard bounds the
current gate's risk — so the refinement is scheduled work, not a
defect.

## Premise corrected (2026-09-07, train chunk 130)

gofresh now exposes the comment/format-insensitive identity
(REQ-closure-canonical-member, v0.97.1) with its derivation on the
fingerprint (REQ-closure-identity-strategy, v0.98.0), and the gate was
built on it and refuted in review: the fingerprint's closure is the
PACKAGE's maximal test-binary closure salted by the subject
(REQ-closure-view-maximal — "an unrelated sibling edit may still stale
them together"), not a reachability-scoped one as the derivation above
assumed. In the gate it sheds every disposition in a package on any
sibling function's semantic edit, and a rename (the salt moves) sheds
every retargeted record on the next measure — motion provably unable to
bear on the mutant's meaning, the over-shedding the middle gate exists
to avoid. The gate stays body hash + operator set. The groundwork
landed: SubjectEvidence records the closure identity's derivation
(`closureStrategy`), so the day the identity is subject-scoped the gate
compares within one derivation and bridges a derivation change.

Lands: user decision — the gate needs a subject-scoped closure identity
on gofresh's fingerprint (closure motion meaning motion reachable from
the target), which no train chunk charters: gofresh states the package
scoping as a deliberate accepted price (REQ-closure-view-maximal), and
the precision band (116–125) narrows observability, not the closure.
Chartering that identity is the user's scope call.
