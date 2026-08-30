# gomutant — tool-resident guidance

## verbs

### run
**does:** Measure mutants and update the findings document.
**knobs:**
- `targets_path` (mcp, cli as `targets`) — path to a targets document (gomutant's or a producer's export); overrides discovery.
- `targets_json` (mcp) — an inline targets document, same formats as targets_path.
- `changed` (mcp, cli) — target only symbols whose bodies differ from this git ref (requires git).
- `budget` (mcp, cli) — candidates per symbol; 0 means exhaustive.
- `timeout_sec` (mcp, cli as `timeout`) — cancel work before the final findings commit; on mcp omitted means 300 seconds and an explicit 0 unlimited, on the cli a duration defaulting to unlimited.
- `oracle_timeout_sec` (mcp, cli as `oracle-timeout`) — maximum duration of each oracle process; 0 (mcp) or the default (cli) means 60 seconds.
- `oracle_memory_mib` (mcp, cli as `oracle-memory-mib`) — memory ceiling per oracle process tree in MiB (GOMEMLIMIT plus a hard data-segment cap): absent or 0 derives RAM/(2 x jobs) floored at 1 GiB, -1 disables; a runaway-allocation mutant dies on its own ceiling as an ordinary kill instead of OOMing the host.
- `jobs` (mcp, cli) — concurrent mutant runs; 0 means half the CPUs.
- `bracket_paths` (mcp, cli as `bracket-path`) — external surfaces the oracle legitimately reads (module-relative paths or absolute files; absolute directories and tool-excluded paths are refused); extends each spawn's observation bracket, carrying the caller's assertion the surface is mutation-free for the run.
- `scratch_namespaces` (mcp, cli as `scratch-namespace`) — in-module run-scratch namespaces DIR:PATTERN (DIR module-relative, PATTERN a single-component os.MkdirTemp-style name pattern): oracle scratch minted and removed inside a namespace stops recording per-run missing-arm noise, forfeiting exactly the appearance-pin of absence-probes the pattern matches; malformed declarations refuse before any measurement. Killed mutants never run test cleanup, so scratch helpers must enforce their own freshness and expect permission-mangled residue.
- `staged` (mcp, cli) — measure the git index snapshot: staged-but-uncommitted content counts clean and the finding records the index tree identity; unstaged drift over a measured target's inputs refuses that target (stage or stash it).
- `force` (mcp, cli) — re-measure even targets whose prior finding still covers the request; the pin spans the mutated symbol's body, every oracle test's source closure, and the observed runtime inputs (toolchain, build configuration, and the other measurement pins are always compared too), so new or changed oracle tests re-measure without force.
- `findings` (mcp, cli) — findings document path (default .gomutant/findings.json), read and updated.
- `packages` (mcp, cli as `package`) — complete package import-path glob filters; * stays within one slash component and ** as a complete component crosses components; alternatives.
- `symbols` (mcp, cli as `symbol`) — complete fully qualified symbol glob filters, for example **/*emitConditions*; alternatives.
- `tags` (mcp, cli as `tag`) — build tags for this call's selection (replaces any ambient GOFLAGS -tags); a go:build-gated symbol or oracle under the tags measures exactly as an untagged one.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection (e.g. go1.26.5); rides the toolchain measurement pin, so a different selection re-measures rather than serving across.
- `vouch` (cli) — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, recorded on the evidence. On mcp vouches are per-server (gomutant mcp --vouch), not per-call.
- `jsonl` (cli) — structured output: every progress event, decision, result row, and summary as one JSON object per line; the human rendering is suppressed.
- `progress-interval` (cli) — cadence of the cumulative progress line (targets committed, candidates, kills, elapsed); 0 disables.
- `plan` (cli) — preflight only: run the full preparation sequence and print every target decision — cached, skipped with reason, or measure with candidate count — then stop before baseline probes and mutant execution, persisting nothing; precondition holes surface before any budget is spent.
- `dir` (cli) — tree root (module or workspace).
**when:** use run for a judged campaign — prior findings with matching
pins are served, the rest re-measure, and each finished target
commits incrementally so an interrupted run keeps completed targets.
Each mutant's oracle executes once, bracketing runtime-input
observation — the bracket a declared external surface extends;
prefer ephemeral for one hand-written probe. Survivors are findings
awaiting disposition, never verdicts. Preparation and decision
streams leave the response when a progress token streamed them —
their totals stay. Long campaigns exceed MCP client timeouts —
raise the timeout or use the cli.
**example:** run with changed=HEAD~1 at a chunk gate; run --plan
first when the target decision set is in doubt.

### discover
**does:** Inspect effective mutation targets without measuring.
**knobs:**
- `targets_path` (mcp, cli as `targets`) — path to a targets document; overrides discovery.
- `targets_json` (mcp) — inline targets document; overrides discovery.
- `changed` (mcp, cli) — changed-scope vs this git ref; empty means the whole tree.
- `packages` (mcp, cli as `package`) — complete package import-path glob filters; alternatives.
- `symbols` (mcp, cli as `symbol`) — complete fully qualified symbol glob filters; alternatives.
- `detail` (mcp) — return every target, oracle-set, and residue row; the default caps each list at 50 with the remainder counted.
- `json` (cli) — render deterministic machine-readable targets.
- `tags` (mcp, cli as `tag`) — build tags for this call's selection.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection.
- `dir` (cli) — tree root (module or workspace).
**when:** use discover to read a campaign's scale before spending
any budget — counts lead the response, each target row carries its
sorted opaque labels, explicit or package-derived oracle mode, and
skip reason, and changed-scope residue rides beside them; exact
oracles are deduplicated in top-level oracleSets, each target's
oracleSet integer referencing oracleSets[].id.
**example:** discover with changed=HEAD~1 before a chunk-gate run.

### findings
**does:** Inspect the findings document: states, survivors, dispositions.
**knobs:**
- `label` (mcp, cli) — show only findings carrying this label.
- `state` (mcp, cli) — show only findings in this judged state: current, stale, unverifiable, or detached (implies judge).
- `judge` (mcp, cli) — re-derive each record's freshness state against the current tree — minutes-class on large documents; a state filter or a tags/toolchain selection implies it; the default reports recorded facts with state 'recorded' and loads no tree.
- `symbol` (mcp, cli) — show only the finding for this mutated symbol.
- `detail` (mcp, cli) — full rows: operator tables, open survivors, attested dispositions, per-candidate unverifiable runtime evidence (candidateEvidence); the default is one bounded summary row per record.
- `findings` (mcp, cli) — findings document path (default .gomutant/findings.json).
- `tags` (mcp, cli as `tag`) — build tags for this call's selection (implies judge).
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection (implies judge).
- `vouch` (cli) — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable); inspection judges under the same acceptances the run used (implies judge). On mcp vouches are per-server.
- `json` (cli) — render deterministic machine-readable findings.
- `dir` (cli) — tree root the default document anchors at.
**when:** use findings to triage without running anything — recorded
facts by default, cheap at any document size; layer is repo
(portable, committed) or local (machine-local overlay, with the
reason it is not committable). Rows cap at 50 with the remainder
counted; the document on disk always carries the full set.
**example:** findings with state=unverifiable after a campaign to
list what cannot serve.

### explain
**surfaces:** mcp
**does:** Why a record stands as it does: clause lists, survivor prescriptions, or the promotion triage.
**knobs:**
- `symbol` — the mutated symbol to explain; empty explains the whole document's promotion state.
- `label` — with no symbol, restrict the promotion triage to findings carrying this label.
- `findings` — findings document path (default .gomutant/findings.json).
- `tags` — build tags for this call's selection.
- `toolchain` — GOTOOLCHAIN directive for this call's selection.
**when:** use explain when a record's state needs acting on — with a
symbol it lists every portable-line clause keeping it machine-local
and each open survivor's execution bucket with the action it
prescribes; without one, the promotion triage groups machine-local
records by failing clause, so an empty committed document explains
itself in one call. Reads the document and tree; runs no tests.
**example:** explain with no symbol when the committed findings
document is unexpectedly empty.

### attest_survivor
**surfaces:** mcp, cli as attest
**does:** Disposition an equivalent surviving mutant with the reasoning on record.
**knobs:**
- `symbol` (mcp, cli) — the mutated symbol.
- `position` (mcp, cli) — the survivor's position (file.go:line:col), as reported.
- `operator` (mcp, cli) — the survivor's operator, as reported.
- `reason` (mcp, cli) — why the mutant is equivalent.
- `findings` (mcp, cli) — findings document path (default .gomutant/findings.json).
- `tags` (mcp, cli as `tag`) — build tags for this call's selection.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection.
- `dir` (cli) — tree root the default document anchors at.
**when:** use attestation only after judging a survivor genuinely
equivalent — refused unless the mutant is among the finding's
current survivors (the provenance guards judge under this call's
selection); the disposition rides re-measures while the
mutated source is unchanged and the mutant keeps surviving, and
sheds when the mutation domain moves (the body or the operator set)
or evidence contradicts it (a test kills the mutant), so every body
version is re-judged.
**example:** attest a compiler-equivalent arithmetic rewrite with
the equivalence argument as the reason.

### prune
**does:** Remove records whose mutated symbol no longer resolves.
**knobs:**
- `check` (mcp, cli) — preview the removals without touching the document.
- `findings` (mcp, cli) — findings document path (default .gomutant/findings.json).
- `tags` (mcp, cli as `tag`) — build tags for this call's selection — HERE the selection decides which records are deleted: a symbol gated behind a tag resolves only under it, so pruning under the wrong selection classifies its records detached and removes them.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection; the same deletion predicate applies.
- `dir` (cli) — tree root the default document anchors at.
**when:** use prune after a refactor for the terminal records no
re-measure can revive — resolution under THIS call's selection is
the deletion predicate, so run it under the selection the records
were measured with; refuses when any package did not load cleanly,
and each removed record's attested dispositions are echoed in the
response, never truncated — the reasoning survives the removal.
Shaped findings (structural targets) are kept unconditionally:
declaration absence is their normal state, never detachment.
**example:** a check preview at a chunk close to lint for dead
records.

### retarget
**does:** Rewrite symbol identity across a rename; dispositions follow their mutants.
**knobs:**
- `from` (mcp, cli) — old symbol prefix: a package pair renames a package (a dot-terminated pass covers its own symbols, a slash-terminated pass its subpackages); a symbol pair renames within its package, segment for segment.
- `to` (mcp, cli) — new symbol prefix, terminated like from.
- `check` (mcp, cli) — preview the rewrites without touching the document.
- `findings` (mcp, cli) — findings document path (default .gomutant/findings.json).
- `tags` (mcp, cli as `tag`) — build tags for this call's selection — each rewritten target must resolve UNDER it, so a tag-gated symbol retargets only under its tag; the wrong selection refuses the batch.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection; the same resolution predicate applies.
- `dir` (cli) — tree root the default document anchors at.
**when:** use retarget after a rename — records whose symbol-bearing
fields carry the from prefix rewrite, surviving attestations follow
their mutants by position, operator, and site, never symbol text,
and each rewritten target must resolve in the current tree under
this call's selection. Rows cap at 50 with the remainder counted.
Run a check preview first.
**example:** a check preview of from=example.com/old.
to=example.com/new. after a package rename, then for real.

### ephemeral
**does:** Run one manual mutant without persisting.
**knobs:**
- `file` (mcp, cli) — tree-relative source file for replacement or edits; omit for batch edits.
- `replacement` (mcp, cli) — the whole replacement source: inline content on mcp, a path to it on the cli.
- `edits` (mcp) — exact-match edits applied sequentially — each old must match exactly once in the content the prior edits produced; state the change, not the file.
- `batch_edits` (mcp, cli as `batch`) — atomic file-scoped exact-match edits ({file, old_string, new_string}); every match resolves against the original file snapshot. Inline on mcp; a JSON path or - for stdin on the cli.
- `test_pkg` (mcp, cli as `test-pkg`) — go package path whose named test decides the kill.
- `run` (mcp, cli) — -run pattern naming the deciding test.
- `timeout_sec` (mcp, cli as `timeout`) — cancel work before attributed result completion; on mcp omitted means 300 seconds and an explicit 0 unlimited, on the cli a duration defaulting to unlimited.
- `oracle_timeout_sec` (mcp, cli as `oracle-timeout`) — maximum duration of the baseline and mutant oracle processes; 0 (the default on both faces) derives the budget from the measured baseline — an explicit value is the override, and the result reports the effective budget and the measured baseline. The advisory coverage probe (an instrumented whole-closure rebuild) shares the derive-mode baseline's measurement leash in both modes.
- `oracle_memory_mib` (mcp, cli as `oracle-memory-mib`) — memory ceiling for the probe's oracle process tree in MiB: absent inherits the server's installed ceiling (mcp), 0 derives RAM/2 floored at 1 GiB, -1 disables; refused while a run is in flight — the campaign owns the process ceiling.
- `runs` (mcp, cli) — run the mutant this many times against the once-probed baseline (1-10, default 1): killed means every run killed — N consecutive kills split a deterministic kill from a property generator's draw luck; per-run verdicts ride the result.
- `attest` (mcp, cli) — record the surviving probe as a judged equivalence with this reasoning, in the committed record beside the findings document (`ephemeral-attestations.json`); refused when the probe killed, was mixed, or never exercised the edit.
- `findings` (mcp, cli) — findings document path whose sibling ephemeral-attestation record `attest` writes (default .gomutant/findings.json).
- `tags` (mcp, cli as `tag`) — build tags for this call's selection.
- `toolchain` (mcp, cli) — GOTOOLCHAIN directive for this call's selection.
- `dir` (cli) — tree root (module or workspace).
**when:** use ephemeral inside the adversarial loop — one
hand-written mutant the operator set cannot generate, one deciding
test, the tree never touched and nothing persisted (an `attest`ed
equivalence judgment is the one durable output, written to the
committed record, never to a finding); an observed
probe executes the named test once, bracketing runtime-input
observation, and a kill carries the killing test's bounded output
head. Give exactly one mutation form.
**example:** ephemeral with a batch edit neutering one guard and
run naming the test that must notice.

### mcp
**surfaces:** cli
**does:** Serve gomutant over MCP.
**knobs:**
- `dir` — tree root (module or workspace).
- `vouch` — dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): every tool call's analysis judges under the server's set — a per-server input because the loaded tree is shared across calls.
**when:** use mcp as the server entry point for an MCP client;
selection (tags, toolchain) is per-call on the served tools, while
vouches bind at the server.
**example:** mcp --vouch pgregory.net/rapid:anyRuneGen under an MCP
client configuration.

### version
**surfaces:** cli
**does:** Print the binary identity and findings document versions.
**knobs:** none
**when:** use version when auditing binary/document compatibility —
it names the findings document version the binary writes and the
range it reads.
**example:** version before judging a findings document another
binary wrote.

### guidance
**does:** Serve this guidance: a verb's full section, or the decision map.
**knobs:**
- `verb` (mcp) — the verb to describe; empty serves the decision map.
**when:** use guidance to learn what a verb does, what a knob
controls, and when to use which — the tool answers from its own
embedded document, so served prose and repository documentation are
the same bytes; the cli takes the verb as its positional argument.
**example:** guidance verb=run; guidance with no verb for
orientation.

## decision map

gomutant measures whether tests notice mutations. The loop: run
measures targets (whole tree, changed vs a git ref, or a targets
document) and maintains the findings document incrementally — prior
findings with matching pins are served, and each decision line says
why; findings inspects the document (survivors with execution
buckets, candidate evidence, repo/local layer) without running
anything — recorded facts by default, cheap at any size; the judge knob
re-derives freshness states (current, stale, unverifiable,
detached) against the tree, minutes-class on large documents, and a
state filter or a tags/toolchain selection implies it;
attest_survivor dispositions an equivalent mutant with the
reasoning on record; prune removes resolved-dead records after a
refactor and retarget follows a rename (both with check
previews); ephemeral probes one hand-written mutant without
persisting a finding — its attest knob records a judged equivalence
in the committed record beside the document; discover lists
effective targets without measuring;
explain answers why — a symbol's full machine-local clause list and
per-survivor prescriptions, or the whole document's promotion
triage. Survivors are findings awaiting disposition — strengthen a
test or attest an equivalence — never verdicts. A survivor bucketed
never-executed wants coverage; executed-and-passed wants a sharper
assertion or an attestation. Send a progress token on run/ephemeral
for phase notifications and a heartbeat; long campaigns exceed MCP
client timeouts — raise the timeout or use the CLI (mcp timeouts
default to 300 seconds, the cli to unlimited). Responses cap long
lists and count the remainder; the findings document on disk is
always complete. MCP-only: explain and inline edit forms; CLI-only:
mcp itself, version, plan preflight, jsonl output, and per-call
vouches (per-server on mcp). The guidance verb serves any verb's
full section — knobs, when-to-use, example — from the tool's own
embedded document.
