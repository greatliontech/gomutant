# The findings report path walks every record's portable line per call

`Store.LayerReasons` and the findings surfaces' layer tally over `Committable` (the report path's layer classification) walk
every merged record's portable line on every call — the same
O(document) evidence-manifest walk the write path no longer pays per
commit — and the store keeps two per-symbol memos of one overlay
entry (the stat-keyed parse cache by entry file, the committability
memo by symbol). The collapse: one entry-state record carrying stat
identity, parse, and verdict, consulted by the report path too.

Lands: cross-tool train chunk 218
