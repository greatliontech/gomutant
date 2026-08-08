# Findings and reason output is unbounded — a bare call returns the whole document

Field report (agent consumer): a bare MCP findings call returned ~131k
characters — 54 records with full operator tables and candidate
evidence each. Per-list caps exist but there is no document-level bound
and no summary view. The CLI is the same: `gomutant findings` says
"list open findings" and prints 1026 lines including operator
breakdowns for fully-killed records.

Wanted shape (stipulator-check's): a bounded summary by default — one
line per record: symbol, state, layer, open count, attested count —
with detail and state/symbol filters opt-in.

Same family: stale reasons embed unbounded test lists — "derived
oracle changed (added: …)" inlines every fully-qualified test name (23
in the field case), repeated per record. A count plus two or three
names carries the signal; the full list belongs behind the detail
view.

Lands: cross-tool train chunk 30 (gomutant consumer-surface bounds and
visibility).
