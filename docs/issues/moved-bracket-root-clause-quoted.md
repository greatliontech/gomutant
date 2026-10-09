# A moved-bracket root carrying the list's framing now spells quoted in its clause

Lands: 302 (the gofresh bump that reads gofresh's quoted-root clause — v0.114.x+; the re-key from the bare root to the quoted one derives from the record alone: `RefusalClause` of a `observation bracket moved: <root>` entry whose root carries a quote, a bracket or the part separator `"; "` becomes `strconv.Quote(root)`).

gofresh 315.C (the chunk's third change set) spells a moved-bracket
refusal's ROOT under the members' quoting rule (memberListName): a root
whose name carries a quote, a bracket or the part separator `"; "`
travels as a Go quoted string, so the split never reads the root's own
segment as the member list (the asymmetry 292 left: a root named like a
labelled member list collided with a plain sibling root). gomutant keys
exemption records on the clause (`RefusalClause`), so an exemption
whose subject is such a root stops matching at gomutant's next gofresh
bump unless the bump's load-time re-key (324.B's `before`/`prepare`
hooks over the exemption record) rewrites the bare root to its quoted
spelling — derivable from the record alone, as the attribution-clause
re-key was. A plain root's clause is unchanged; NUL, CR, LF and invalid
UTF-8 never reach a root (refused at declaration).
