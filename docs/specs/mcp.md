# The MCP server

An agent drives gomutant the way an operator drives the CLI: measure, read
findings, disposition survivors, probe manual mutants. The server is a shell
over the same library — one engine, two faces — and it inherits the advisory
stance whole: no tool renders a pass/fail verdict (REQ-result-findings).

**REQ-mcp-tools** (behavior): The MCP server MUST expose the library's
operations as tools — measuring a target set (every producer form: discovery,
changed scope, a targets document in gomutant's or a parsed producer's
format), discovering targets without running, reading open findings grouped
by label, dispositioning a survivor, and running an ephemeral mutant — each
a thin shell over the same library. The server is the primary face: the CLI
is a subset over the same library, so nothing either face does bypasses the
engine's rules — but a tool may exist server-side first.

**REQ-mcp-findings-doc** (behavior): The server MUST maintain the same
findings document the CLI maintains — a measuring tool merges fresh findings
over the prior document by symbol and an attesting tool rewrites it — so an
agent session and an operator session compose through one record, and
neither invalidates the other's dispositions.

**REQ-mcp-ephemeral-edits** (behavior): The ephemeral tool MUST accept the
mutant as either a whole replacement source or as exact-match edits applied
to the file's current content (REQ-exec-ephemeral) — an agent hand-crafting
a mutation states the change, not the file — and MUST return the applied
result's evidence identically in both forms. Edits apply sequentially: each
matches against the content the prior edits produced, exactly once, so a
statement of changes reads top to bottom and an ambiguity introduced by an
earlier edit is refused like any other.
