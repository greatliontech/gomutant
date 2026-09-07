# The MCP server

An agent drives gomutant the way an operator drives the CLI: measure, read
findings, disposition survivors, probe manual mutants. The server is a shell
over the same library — one engine, two faces — and it inherits the advisory
stance whole: no tool renders a pass/fail verdict (REQ-result-findings).
The MCP surface outranks the CLI in design priority: it serves an LLM agent
in a harness, where every byte of output spends the consumer's context —
minimal output, maximal usefulness governs every response shape.

**REQ-mcp-tools** (behavior): The MCP server MUST expose the library's
operations as tools — measuring a target set (every producer form: discovery,
changed scope, a targets document in gomutant's or a parsed producer's
format), discovering targets without running, inspecting findings (recorded
facts by default — the judged freshness classification is opt-in per
REQ-result-inspection) with
optional opaque-label filtering, explaining a record or the document's
promotion state, dispositioning a survivor, and running an ephemeral mutant — each
a thin shell over the same library. The server is the primary face: the CLI
is a subset over the same library, so nothing either face does bypasses the
engine's rules — but a tool may exist server-side first.
Run and discovery tools expose the same package and symbol filters as the
library (REQ-target-filtering); run results expose the same aggregate summary
as the CLI (REQ-exec-run-status), with preparation events and decisions
riding the response per REQ-mcp-envelope — streamed to notifications or
inline-capped, their totals always exact. A run request carrying an MCP progress token
additionally receives progress notifications forwarded from the preparation
events, target decisions, and advisory freshness-analysis events — the
keep-alives and the payload-bearing diagnostics alike (the analysis-event
class in [execution.md](execution.md): subscribing delivers both kinds);
an ephemeral request's notifications carry its preparation events — the
baseline probe, each mutant run, the coverage probe — and the heartbeat
names the phase in flight.
Notification delivery is advisory and never changes tool results or errors.
Discovery encodes exact effective oracles without repeating them: the result
contains canonical top-level `oracleSets` with zero-based integer `id` values,
and each target carries the `oracleSet` id whose `oracle` array it uses. Oracle
sets are assigned in first-target order, so every RETAINED target row's
reference resolves within the equally-capped set list, and expanding the
retained references yields the same ordered target descriptions as library
and CLI inspection of those targets; sets beyond the cap are referenced only
by omitted target rows.

**REQ-mcp-envelope** (behavior): Tool responses MUST be bounded for their
actual consumer — an agent paying per token. Counts lead: discovery reports
its target, skipped, and residue totals before any row, and row lists cap
(target and residue rows at 50 unless `detail` is requested; run finding rows
at 50, open survivors per finding at 20 — a changed-ref run's on-delta
survivors as their own list under the same bound; findings-inspection rows at 50,
rendered as one summary row per record — symbol, state, reason, layer, run
identity, open and attested counts — with the full rows behind `detail` and
the roster narrowable by state, by symbol, and by run identity) with the omitted remainder counted,
never silently dropped — the findings document on disk always carries the
full set and the response names its path. Preparation events and target
decisions are progress data, not result data: a request carrying a progress
token receives them as notifications and the response keeps only their
totals; a request without one keeps them inline, capped, with honest totals.
Candidate evidence is drill-down via the findings tool, never run payload.
Advisory lists — oracle guidance, attestation sheds and carries,
attestation contradictions, property-oracle statements, discovery's oracle
sets, the findings response's ephemeral-equivalence attestations, the run
and findings responses' preserved legacy overlay rows — cap at
the same row bound with their remainders counted (the
error-riding shed fold keeps its own exemplar bound over the full set).
And an empty answer is an answer, never a bare zero-row success: a run or
discovery selecting zero targets, a findings query matching no record (the
state filter's judged drop included), an explain triage over an empty
document or a label matching nothing, and a retarget prefix matching
nothing each carry a note naming the input that emptied the result and the
caller's next step; and every whole-tree reconcile that dropped records
states the drop count — the document write is the response's to own
whether or not anything was measured — so the caller's next move is a
decision, not a diagnosis. A selection mode that emptied the target set
before filters applied is never blamed on the filters.
While a token listens, a heartbeat notification names the current phase and
elapsed time on the one fixed cadence every face's progress keeps (the
value REQ-exec-run-status fixes for the CLI progress line), so no compile
or execution stretch stays silent past a client's deadline. The server's
instructions and each tool's description teach when to use what and what
the caps mean.

**REQ-mcp-surfaces** (behavior): Each verb's default behaviour, output,
and values MUST be the ones its surface's reader is served by — the MCP
face an agent paying per token, the CLI face a person at a terminal — as
the table below records. The table's verbs and faces are the guidance
document's, and every bound and timeout it states is the one the
envelope policy and the command deadline hold (enforced by the
surface-table pin).

| verb | faces | CLI default | MCP default | shared values |
| --- | --- | --- | --- | --- |
| run | mcp, cli | a human progress line on the shared cadence, then the report; `--json` the JSON-lines stream; `--plan` the preflight; the command timeout unlimited | counts lead; finding rows capped at 50 with open survivors at 20 per record; preparation events and decisions as notifications under a progress token (a heartbeat on the shared cadence), inline and capped without one; the command timeout 300 seconds | oracle timeout 0 derives each group's budget from its measured baseline; vouches per call on the CLI, per server on MCP |
| discover | mcp, cli | a human table; `--json` the target document | counts lead; target, oracle-set, and residue rows capped at 50 unless `detail`; oracle sets referenced by id | one target source: the tree, `changed`, or a targets document |
| findings | mcp, cli | a human summary with the ephemeral-attestation count and the layer counts; `--json` the complete finding rows (the ephemeral-attestation record is the file beside the document, which a CLI reader has) | one summary row per record capped at 50; the ephemeral attestations inline (an MCP reader has no file) capped at 50; the layer counts | `detail` for full rows on both faces; `judge` re-derives freshness (a state filter or a selection implies it); filters by state, symbol, label |
| explain | mcp | — | a symbol's causal record, or the document's promotion triage; groups capped at 50, symbols per group at 10, open survivors and clauses at 20 | — |
| attest_survivor (CLI `attest`) | mcp, cli | the recorded echo with the record's layer and its judged state | the same | symbol, position, operator, reason |
| prune | mcp, cli | the removals; `--check` previews | the removals, never truncated (the lifecycle exception to the row bound, REQ-mcp-lifecycle); `check` previews | the selection decides which records are dead |
| retarget | mcp, cli | the rewrites; `--check` previews | the rewrites capped at 50; `check` previews | `from` and `to` terminated alike |
| ephemeral | mcp, cli | `--file` with a `--replacement` path, or `--batch` a JSON file `{"edits":[{"file","old_string","new_string"},…]}`; a progress line on the shared cadence; the command timeout unlimited | inline `replacement`, `edits`, or `batch_edits`; notifications and a heartbeat under a token; the command timeout 300 seconds | exactly one mutation form; runs 1–10; oracle timeout 0 derives the budget; `attest` records a judged equivalence |
| guidance | mcp, cli | a verb's section, or the orientation | the same | — |
| mcp | cli | serves the tools over stdio | — | per-server vouches |
| version | cli | the binary and document versions | — | — |

**REQ-mcp-guidance** (behavior): Tool-level served prose MUST be the
embedded guidance document's projections (`docs/guidance.md`, in the
fleet format gofresh's guidance spec defines): every tool description
and CLI Short/Long is the document's rendering for that surface and
spelling, the server instructions are the decision map verbatim, and
a `guidance` tool (and CLI command, its verb positional) serves a
verb's full section or, verbless, the decision map — refusing an
unknown verb with the decision map named as the way to enumerate.
Both surfaces bind the per-surface coverage judgment: every listed
tool and schema property, and every visible leaf command and local
flag, documented exactly, both directions — cobra's help and
completion plumbing is surface plumbing outside the judgment. The
document's knob prose is the authoritative superset; per-parameter
schema and flag usage strings stay terse wire detail, and a schema
or usage string contradicting the document is a defect of whichever
is wrong.

**REQ-mcp-findings-doc** (behavior): The server MUST maintain the same
findings document the CLI maintains — a measuring tool merges fresh findings
over the prior document by symbol and an attesting tool rewrites it — so an
agent session and an operator session compose through one record, and
neither invalidates the other's dispositions. What a run surface reports is
the post-merge record: survivor rows, disposition rows, and summary counts
describe the document the run left behind, on both faces, so the response
and the document never disagree about a disposition's fate. A run that
carries any record from the machine-local overlay into the committed
document says so — the promoted count is a document change git only sees
when committed.

**REQ-mcp-lifecycle** (behavior): The server MUST expose the prune and
retarget verbs of REQ-result-lifecycle in [results.md](results.md) as
tools with check previews. The prune response's removal echo is never
truncated - for an overlay-resident record it is the disposition
reasoning's last home, and a capped preview would hide part of what the
destructive call deletes - the one sanctioned exception to
REQ-mcp-envelope's row caps; retarget rows cap with counted omissions
as usual.

Concurrent runs against different findings documents are legal, and so
are probes beside them: a run's oracle bounds — its inner-parallelism
width and its memory ceiling (REQ-exec-oracle-parallelism and
REQ-exec-oracle-memory in [execution.md](execution.md)) — are the run's
own value, handed to every oracle process it spawns, never process state
another call could move, so a concurrent campaign or probe with a
different job count or ceiling runs under its own and leaves the first
run's oracles, evidence environment, and recorded pin exactly as that
run derived them.

**REQ-mcp-liveness** (behavior): The server MUST detect a dead client
transport while a campaign is in
flight and cancel the campaign rather than measure detached: it
keepalive-pings the session, and a failed ping write cancels every
in-flight request context, which aborts the run under
REQ-exec-cancellation's terms within the ping interval. A client that
abandons a request while its connection lives owes a cancellation
notification per the protocol - a ping cannot see intent - and the
campaign lock of REQ-exec-exclusivity keeps any surviving detached
campaign from interleaving with a retry. A live client that merely
misses the ping's response budget loses its session while the campaign
completes and commits: the document, not the response, is the result
of record.

**REQ-mcp-explain** (behavior): The server MUST expose an explain tool
answering causally, from the findings document and current-tree
inspection alone — no tests run, the advisory stance intact: given a
mutated symbol, the record's inspection state and reason, its
persistence layer with every portable-line clause it fails
(REQ-result-layers' line — the full list, never only the first, so
repairing one clause never surfaces the next as a surprise;
machine-local input clauses sharing a top-level directory roll up into
one row naming the root and the path count — a tempdir-heavy oracle
otherwise repeats one story per leaked path — with the per-path list
still derivable from the document), each
open survivor with its execution bucket and the action the bucket
prescribes, and the attested count; given no symbol, the promotion
triage — repo and machine-local counts leading, machine-local records
grouped by failing clause with their symbols, restrictable by opaque
label — so an empty committed findings document explains itself in one
call. Every row set caps with counted omissions per REQ-mcp-envelope
(open survivors and clause rows at 20, clause groups at 50 with 10
symbols each, groups ordered by count then reason), the clause list
included — the full-list rule says no clause is silently *replaced*
by an earlier one, and a counted omission is not silent. Candidate evidence stays drill-down via the findings tool
(REQ-mcp-envelope); the record-level reason names candidate-local
evidence when it is the cause. An unknown symbol refuses, naming the
findings tool as the roster.

**REQ-mcp-ephemeral-edits** (behavior): The ephemeral tool MUST accept the
mutant as a whole replacement source, sequential exact-match edits applied
to one file, or an atomic batch of file-scoped exact-match edits applied to
one original multi-file snapshot (REQ-exec-ephemeral) — an agent hand-crafting
a mutation states the change, not whole files — and returns the applied
result's evidence identically in every form: the verdict with the kill's
bounded output evidence and, under `runs`, the per-run verdicts
(REQ-exec-ephemeral). Single-file edits apply sequentially: each
matches against the content the prior edits produced, exactly once, so a
statement of changes reads top to bottom and an ambiguity introduced by an
earlier edit is refused like any other. Batch edits carry their own paths and
the top-level single-file path is absent; every batch path is confined to the
server tree before the library resolves the atomic snapshot.
