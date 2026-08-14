# Long-running MCP server refuses the document a newer CLI writes

Lands: cross-tool train chunk 41 (the MCP surface contract audit:
a version-ahead refusal names its probable cause and the
restart/upgrade signal rather than a bare range error).

## Observed (2026-08-14, ocifs)

A `gomutant mcp` process started before a binary upgrade (its
in-memory DocumentVersion 6, reading 4–6) served a repo whose
campaigns were run by the post-upgrade CLI at the same path
(~/.local/bin/gomutant, writing version 7). Every `findings` call
answered `findings document version 7 not understood (want 4-6)` —
correct per REQ-result-export, but the MCP surface stayed dead until
someone realized the server process itself was stale and restarted
it. This recurred across three sessions on the same machine (the
server is long-lived; the CLI upgrades under it).

The refusal message names the versions but not the likely cause; a
reader pointed at the document suspects corruption (this consumer
initially recorded it as an empty-write bug — see
campaign-persists-zero-findings-on-dirty-trees.md for what the
document actually contained). A version *ahead* of the reader's
range is nearly always "a newer gomutant wrote this" — the message
could say so, and the server could stat its own binary against its
start time (or compare the on-disk binary's advertised version) and
surface "server is older than the CLI that wrote this document;
restart it" through the MCP tool result.
