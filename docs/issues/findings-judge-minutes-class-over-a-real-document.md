# `findings --judge` over a real document runs past two minutes

Field report from the protodb session (2026-10-03, the standing channel):
`findings --judge` over protodb's committed document — 196
repo-committable and 127 machine-local records — took over 120 seconds
through the MCP face (the call moved to the background), where the judged
inspection is described as seconds-class. The tree was at gomutant
cd6a082 with gofresh v0.108.2 (before the bump that bounds the
dynamic-state discharge by the analysis budget); a second timing point
after 9afc5c6 (gofresh v0.108.3) is offered, with the run log's phase
lines.

The current judged walk already shares admission and view sets per posture;
`viewsFor` refuses prebuilt views of the wrong mode and constructs a supplementary
set only for missing subjects. This is not evidence of a general one-view-per-
record construction bug. Measure the residual load, analysis, supplementary
views, manifest reading and rendering with the same document and tree. Separate
cold and warm cost and policy-only refusals from actual freshness judgment.
The offered second timing point has not been executed for this update.

Secondary constituent: tooling-redesign 7.8 owns any demonstrated redundant
generation-scoped work; existing sharing must not be rebuilt under another name.

Lands: tooling-redesign 7.7.
