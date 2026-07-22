# The MCP server

An agent drives gomutant the way an operator drives the CLI: measure, read
findings, disposition survivors, probe manual mutants. The server is a shell
over the same library — one engine, two faces — and it inherits the advisory
stance whole: no tool renders a pass/fail verdict (REQ-result-findings).

**REQ-mcp-tools** (behavior): The MCP server MUST expose the library's
operations as tools — measuring a target set (every producer form: discovery,
changed scope, a targets document in gomutant's or a parsed producer's
format), discovering targets without running, inspecting findings with
optional opaque-label filtering, dispositioning a survivor, and running an ephemeral mutant — each
a thin shell over the same library. The server is the primary face: the CLI
is a subset over the same library, so nothing either face does bypasses the
engine's rules — but a tool may exist server-side first.
Run and discovery tools expose the same package and symbol filters as the
library (REQ-target-filtering); run results expose the same aggregate summary
as the CLI (REQ-exec-run-status), with preparation events and decisions
riding the response per REQ-mcp-envelope — streamed to notifications or
inline-capped, their totals always exact. A run request carrying an MCP progress token
additionally receives progress notifications forwarded from the preparation
events, target decisions, and advisory freshness-analysis keep-alive events;
an ephemeral request's notifications are coarse tool-boundary messages.
Notification delivery is advisory and never changes tool results or errors.
Discovery encodes exact effective oracles without repeating them: the result
contains canonical top-level `oracleSets` with zero-based integer `id` values,
and each target carries the `oracleSet` id whose `oracle` array it uses. Oracle
sets are assigned in first-target order, so expanding each reference yields the
same ordered target descriptions as library and CLI inspection.

**REQ-mcp-envelope** (behavior): Tool responses MUST be bounded for their
actual consumer — an agent paying per token. Counts lead: discovery reports
its target, skipped, and residue totals before any row, and row lists cap
(target and residue rows at 50 unless `detail` is requested; run finding rows
at 50, open survivors per finding at 20) with the omitted remainder counted,
never silently dropped — the findings document on disk always carries the
full set and the response names its path. Preparation events and target
decisions are progress data, not result data: a request carrying a progress
token receives them as notifications and the response keeps only their
totals; a request without one keeps them inline, capped, with honest totals.
Candidate evidence is drill-down via the findings tool, never run payload.
While a token listens, a heartbeat notification names the current phase and
elapsed time on a fixed cadence, so no compile or execution stretch stays
silent past a client's deadline. The server's instructions and each tool's
description teach when to use what and what the caps mean.

REQ-mcp-envelope: enforced by `TestRunStreamsLeaveThePayloadWhenStreamed`,
`TestDiscoverCountsLeadAndRowsCap`, and
`TestRunResponseCarriesNoCandidateEvidenceField`.

**REQ-mcp-findings-doc** (behavior): The server MUST maintain the same
findings document the CLI maintains — a measuring tool merges fresh findings
over the prior document by symbol and an attesting tool rewrites it — so an
agent session and an operator session compose through one record, and
neither invalidates the other's dispositions.

**REQ-mcp-ephemeral-edits** (behavior): The ephemeral tool MUST accept the
mutant as a whole replacement source, sequential exact-match edits applied
to one file, or an atomic batch of file-scoped exact-match edits applied to
one original multi-file snapshot (REQ-exec-ephemeral) — an agent hand-crafting
a mutation states the change, not whole files — and returns the applied
result's evidence identically in every form. Single-file edits apply sequentially: each
matches against the content the prior edits produced, exactly once, so a
statement of changes reads top to bottom and an ambiguity introduced by an
earlier edit is refused like any other. Batch edits carry their own paths and
the top-level single-file path is absent; every batch path is confined to the
server tree before the library resolves the atomic snapshot.
