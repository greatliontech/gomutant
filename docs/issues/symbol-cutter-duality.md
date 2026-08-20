# Two symbol cutters over one grammar

symbolPackage (freshness.go: first dot after the last slash — correct
for every symbol class, methods included) and splitTestSymbol
(gomutant.go: last dot — correct for plain test symbols, wrong for a
method-valued symbol) cut the same symbol grammar with the
relationship unstated. splitTestSymbol's callers today feed it only
test-function symbols (killers, oracle scoping), so no reachable
input breaks it — but nothing pins that boundary, and a
method-valued oracle symbol would silently mis-split. Candidate: one
cutter (symbolPackage + a name-half accessor) with splitTestSymbol
expressed on top, or a stated input-class contract on each.

Lands: with the next change touching either cutter's callers.
