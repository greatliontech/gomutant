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

Second field report, two rendering-correctness findings folded here:

- Run output renders the PRE-SPLICE measurement while the document holds
  the post-splice truth, both directions observed: a campaign printed 8
  survivor lines and "0 attested, 8 open" while the document recorded 4
  open + 4 attested (served attestations re-attached during the splice);
  a scoped run's MCP response listed a mutant in the finding's `open`
  array while attest_survivor refused it as already attested. The
  operator re-adjudicates attested survivors or believes judgments were
  lost; the harness-facing response was wrong while the disk was right.
  Fix: survivor lines, the open array, and summary counts render from
  the post-splice record (or pre-splice lines are labeled raw
  measurement). Repro: measure a target with a survivor, attest, force a
  re-measure by touching the test.
- Unstable-oracle guidance names the bracket root, not the mover: 37
  survivors bucketed unstable with "observation bracket moved:
  internal/archive" when the actual churn was per-test temp dirs under
  testdata/scratch — "stabilize the input named in the reason" is not
  actionable when the name is the package dir. Fix: on union-equality
  failure, report the narrowest diverging path(s) between execution
  observations. Repro: an oracle test that MkdirTemps and removes a
  subdir under its own package.

