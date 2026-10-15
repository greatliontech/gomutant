# A findings record is keyed by its symbol alone, so two build selections over one document re-measure each other

A record carries its oracle's evidence as subject fingerprints under the
selection that measured it. A run under another selection derives
different fingerprints for the gated oracle tests (their closures
include the selection constant), so it sees oracle drift and
re-measures. Current full-oracle@3 remeasures the complete required oracle on
an oracle change; it does not run only added or moved tests or carry old kills
through that change. Different selections can still replace one another's
record because the document remains symbol-unique.

Historically measured at chunk 284 (2026-09-30) over gomutant's own tree: the root
package covered 89.4% of its statements under the integration selection
and 59.9% under the default one — 1,601 of 4,958 covered blocks and 97
of 514 covered functions reached zero under the default selection, whose
campaign was the affordable one (seconds per mutant against about forty
minutes per survivor under the integration oracle). The report described mutants
in those blocks as never-executed survivors prescribed wants-coverage while
their killers sat behind the gate. Those historical labels do not establish
absence of execution or compile-time influence. The selection coexistence defect
remains even after retiring their execution authority.

The required lifecycle supports coexisting records by symbol and complete
selection identity — the document holding one record per selection it was measured under, the
faces reading the selection's records, serving and drift judged within
a selection, and a survivor under one selection carrying the other's
verdict where one exists. REQ-result-record's key sentence and the
document's tables change together across both layers, filtering, serving,
attestation, prune and retarget. The current write version is 16 and the
current parser enforces symbol uniqueness. Choose a new format from that live
state, not the old version-14 reservation. Decoding history grants no execution
authority; policies 1 and 2 require whole remeasurement under REQ-result-stale.

Secondary constituent: tooling-redesign 9.2 settles the final envelope format.

Lands: tooling-redesign 6.6.
