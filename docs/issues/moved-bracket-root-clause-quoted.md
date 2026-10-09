# A moved-bracket root carrying the list's framing now spells quoted in its clause

Lands: 302 (the gofresh bump that reads gofresh's quoted-root clause — v0.114.x+; the re-key is ONE call: a stored `observation bracket moved: …` clause becomes `runtimeinput.CanonicalMovedBracketClause(clause)`, which is idempotent — an already-canonical key comes back unchanged however often the load runs — never a gomutant copy of the quoting predicate, which would be two rules for one grammar).

gofresh 315.C (the chunk's third change set) spells a moved-bracket
refusal's ROOT under the members' quoting rule (memberListName): a root
whose name carries a quote, a bracket or the part separator `"; "`
travels as a Go quoted string, so the split never reads the root's own
segment as the member list (the asymmetry 292 left: a root named like a
labelled member list collided with a plain sibling root). gomutant keys
exemption records on the clause (`RefusalClause`), so an exemption
whose subject is such a root stops matching at gomutant's next gofresh
bump unless the bump's load-time re-key (324.B's `before`/`prepare`
hooks over the exemption record) rewrites the stored clause through
`runtimeinput.CanonicalMovedBracketClause` (exported at 315.C for
exactly this: the quoting predicate stays gofresh's one rule, and the
call is idempotent — a canonical key is returned unchanged, so a load
running twice never quotes a key twice). The one shape the text cannot
resolve — a legacy bare root whose own name is a valid Go quoted
literal of a framing-bearing name — reads as canonical and is not
re-keyed, so that exemption stops matching (gofresh records it as the
text channel's residual class). A plain root's clause is unchanged;
NUL, CR, LF and invalid UTF-8 never reach a root (refused at
declaration).
