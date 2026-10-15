# The findings report path walks every record's portable line per call

`Store.LayerReasons` and the findings surfaces' layer tally over `Committable` (the report path's layer classification) walk
every merged record's portable line on every call — the same
O(document) evidence-manifest walk the write path no longer pays per
commit — and the store keeps two per-symbol memos of one overlay
entry (the stat-keyed parse cache by entry file, the committability
memo by symbol). The collapse: one entry-state record carrying stat
identity, parse, and verdict, consulted by the report path too.

The current report path remains separate from the write's stored routing.
Measure which repeated work survives existing caches before merging them.
A shared entry state must key every decisive input, including layer/document
generation and external evidence used by the verdict; a file stat alone cannot
certify all of those. Reuse a write's routing where that is the question, but
retain whole-inventory inspection and current re-judgment where requested.

Lands: tooling-redesign 7.8.
