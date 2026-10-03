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

What the judged walk pays per record is the question: the freshness
check builds a view per record's subjects — the pass whose discharge
analysis the budget now bounds, memo-served for an unchanged package —
and the posture pass judges every record against the current tree; a
walk that re-derives per record what one view set would derive once is
the same shape 156.4a collapsed for the campaign ("one admission pass
and one view set per posture"). The second timing point and the phase
lines separate the freshness pass from the document walk.

Lands: cross-tool train chunk 302 (gomutant — the findings record and
its document walk are its domain; the open triages this with protodb's
second point).
