# The CLI progress line and the MCP heartbeat name the stretch in flight from two sources

REQ-exec-run-status says the CLI face keeps a cadenced progress line
naming the stretch in flight and the MCP heartbeat names the same
stretch. The two are built from different sources and vocabularies:
the CLI reporter's phase is fed by PreparationEvent.Text plus
hand-written literals ("loading", "preparing" in lifecycle.go, run.go,
ephemeral.go) and yields to tallies at the first decision, while the
MCP's stretch is ExecutionEvent.Stretch plus four run-tool literals
("loading tree", "selecting targets", "merging findings", "rendering
the response"). One stretch vocabulary — a root type both faces
record and render — would collapse the literals and let the CLI's
pre-decision line and the MCP heartbeat come from one place, as the
four advisory grammars now do.

Lands: cross-tool train chunk 213 (the seams chunk).
