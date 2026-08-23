# Repo-layer findings document at 70 MB — growth is unbounded per campaign

Surfaced by a consumer push (tugboat, 2026-08-24): GitHub warns
GH001 on .gomutant/findings.json at 70.41 MB, past the 50 MB
recommended maximum, after one transport-tier --changed campaign
(37 targets, 2134 generated) merged into the store. The v3 repo
layer accretes full candidate and kill evidence per record and a
store-wide campaign multiplies it; nothing bounds or compacts the
document, so every consumer repo carrying campaigns trends toward
unclonable.

## Fix directions

Structural, one or a combination: a compact encoding (the JSON is
highly repetitive — dictionary or columnar layout, or a
zstd-container the tools read natively); evidence tiering (verdict
rows stay in the repo layer, bulky kill-output heads route to the
machine overlay the way dirty-tree evidence already does);
generation pruning (superseded records' evidence dropped once a
newer measurement covers the pin). Whichever lands, the repo layer
must stay the committable record of verdicts (REQ-result-layers) —
the cut is evidence bulk, never verdict rows.

Lands: with the tool phase's gomutant visit (queued with the
growth-serve verdict-integrity fix), or the first consumer refusing
a push on the document's size.
