# Tag-gated and toolchain'd oracles are outside the measurable surface

gomutant is single-selection by construction: one ambient environment
feeds the tree load, target discovery, oracle resolution, and the
oracle spawns, so there is no stipulator-style divergence between an
execution view and a resolution view - and the toolchain is a
measurement pin, so an ambient toolchain change re-measures instead of
serving stale evidence. The boundary: a `//go:build`-gated oracle (or
one needing a selection toolchain, e.g. a DST leg's stdlib) is
invisible end to end - not loaded, not discoverable, not runnable - so
mutation adequacy of an invariant whose only enforcement is a
tag-gated arm cannot be measured. A fix is a build-selection surface
(tags + toolchain on Options/CLI/MCP) applied consistently to the
load, discovery, oracle spawns, and the measurement pins - the
consistency that today comes free from having no surface at all.
Stipulator's REQ-go-build-selections (view = the (tag-set, toolchain)
pair, first-declaring-view precedence, degrade floor) is the reference
shape for the resolution half.

Lands: a campaign needs a tag-gated or toolchain'd oracle.
