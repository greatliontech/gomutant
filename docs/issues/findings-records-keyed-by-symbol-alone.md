# A findings record is keyed by its symbol alone, so two build selections over one document re-measure each other

A record carries its oracle's evidence as subject fingerprints under the
selection that measured it. A run under another selection derives
different fingerprints for the gated oracle tests (their closures
include the selection constant), so it sees oracle drift and
re-measures — and a drift re-measure runs the added and moved oracle
tests, which under the default selection skip, turning the integration
selection's kill into a survivor. Two selections over one document
thrash, and the losing direction is verdict-flipping.

Measured at chunk 284 (2026-09-30) over gomutant's own tree: the root
package covers 89.4% of its statements under the integration selection
and 59.9% under the default one — 1,601 of 4,958 covered blocks and 97
of 514 covered functions reach zero under the default selection, whose
campaign is the affordable one (seconds per mutant against about forty
minutes per survivor under the integration oracle). Every mutant in
those blocks is a never-executed survivor in the committed document,
which `explain` prescribes as wants-coverage while its killer sits
behind the gate: neither honestly strengthenable nor attestable, and
the integration selection's verdict for it cannot be persisted beside
the default's.

The structural answer: a record keyed by (symbol, selection) — the
document holding one record per selection it was measured under, the
faces reading the selection's records, serving and drift judged within
a selection, and a survivor under one selection carrying the other's
verdict where one exists. REQ-result-record's key sentence and the
document's tables change; the document version bumps.

Lands: cross-tool train chunk 302 (chartered at 284's tick).
