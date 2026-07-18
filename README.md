# gomutant

A mutation tester for Go. gomutant breaks a target symbol's body on purpose,
runs the tests that vouch for that symbol against each mutant, and reports
the mutants no test caught. A survivor is a finding: either the test is weak
and should be strengthened, or the mutant is equivalent and should be
attested as such. gomutant measures whether tests have teeth; it never
decides whether anything is "covered" — that judgment belongs to whatever
consumes its findings.

The contract lives in [docs/specs](docs/specs/overview.md).

## CLI

```
# Measure every function in the tree against its package's tests.
gomutant run

# Bound command work through result commit separately from each oracle process.
gomutant run --timeout 2h --oracle-timeout 2m

# Measure only what changed since a ref, budgeted for the hot loop.
gomutant run --changed HEAD --budget 5

# Explicit targets (symbol + oracle + labels) from a JSON document.
gomutant run --targets targets.json

# Inspect current, stale, unverifiable, and detached findings.
gomutant findings

# Inspect effective symbols, derived or explicit oracles, labels, and residue.
gomutant discover --changed HEAD

# Preview and run one selected surface. Filters are repeatable globs;
# alternatives within one kind are ORed, package and symbol kinds are ANDed.
gomutant discover --package 'example.com/project/**' \
    --symbol 'example.com/project/parser.*'
gomutant run --package 'example.com/project/**' \
    --symbol 'example.com/project/parser.*'

# Disposition a survivor as equivalent, with the reasoning on record.
gomutant attest --symbol example.com/pkg.F --position f.go:10:5 \
    --operator "return: zero" --reason "result unused on this path"

# Run an atomic agent-authored edit batch without touching the tree.
gomutant ephemeral --batch edits.json --test-pkg example.com/pkg \
    --run '^TestF$'
```

Findings live in a versioned JSON document (default
`.gomutant/findings.json`), pinned to the inputs that produced them — the
target and oracle source closures, observed runtime inputs, toolchain and
build configuration, operator-set version, budget, and effective oracle timeout. A
run re-measures exactly what a moved pin invalidates and serves the rest from
the document. Open findings are survivors minus attested dispositions;
whether they fail a build is the caller's policy, not gomutant's verdict.

## Standalone workflow

Inspect before measuring when selecting a changed or filtered surface. The
`discover` command resolves the same targets and explicit or package-derived
oracles as `run`; invalid patterns, ambiguous oracles, duplicate symbols, and
filters matching nothing are refused by both. Changed-scope inspection also
lists every changed path that cannot produce a target and why.
Patterns match the complete package path or symbol. `*`, `?`, and character
classes stay within one slash-separated component; a complete `**` component
crosses zero or more components; braces provide alternatives.

Use an explicit target document for a stable repeatable matrix. This repository
dogfoods that path with:

```
gomutant discover --targets testdata/self-host-targets.json
gomutant run --targets testdata/self-host-targets.json --jobs 1 \
    --timeout 30m --oracle-timeout 10m
```

A run streams deterministic preparation for loading, target resolution,
freshness, mutant generation, and baseline probes, then reports every ordered
target decision before launching mutants: `cached`, `skipped`, or `measure`
with the selected candidate count and `no-prior`, `forced`, `budget`, or
`stale`. Budgets select a deterministic candidate prefix before no-op,
duplicate, or compile discards. It finishes with deterministic per-target
findings and aggregate
generated, discarded, killed, survived, attested, and open totals. Repeating
the same run serves findings whose pins still hold; `--force` deliberately
remeasures them. Package- and symbol-filtered runs are scoped and never delete
findings outside their selected surface. `--timeout` bounds command work through
the atomic findings commit and defaults to unlimited; `--oracle-timeout` bounds
each baseline or mutant oracle process and defaults to one minute. Each
finished target's finding commits incrementally under the findings document
lock, so an interrupt or command timeout observed mid-run cancels the full
oracle process tree while keeping every already-finished target; an unfinished
target commits nothing. Once the final commit succeeds, success wins
and final output completes without rollback. Only the oracle timeout is a
finding freshness pin.
Before fresh mutant execution, each distinct oracle group must pass on the
unmutated tree with a stable test count and result. Runtime-input movement makes
the completed finding unverifiable and therefore ineligible for reuse; an
already-failing or structurally unstable baseline refuses the measurement.
A mutant process that cannot prove its runtime-input log complete — a panic,
timeout, or compile rejection — marks only its own candidate's evidence
unverifiable: the record still serves its covered candidates on a later run,
re-executing exactly the flagged candidates under a passing current baseline
probe (reported as a `cached` decision carrying the re-executed candidate
count), while an incomplete baseline observation stays finding-wide and
remeasures the whole target.
Mutation execution is supported on Unix and Windows hosts; other hosts are
refused during tree loading rather than run with weaker cleanup guarantees.

Review survivors independently of process success:

```
gomutant findings
gomutant attest --symbol example.com/pkg.F --position f.go:10:5 \
    --operator 'return: zero' --reason 'result unused on this path'
```

Strengthen a test for every non-equivalent survivor and rerun its target. Use
an attestation only when the mutant is behaviorally equivalent, stating why;
attestations remain visible on a stale record, but a remeasurement sheds them
when the evidence pins move. Open survivors remain advisory, so a completed mutation run exits
successfully regardless of their count. Operational errors and malformed or
unattributable observations fail the command; cancellation observed before the
result commit does too.

## MCP

Agents drive gomutant over the Model Context Protocol — `gomutant mcp` serves
stdio with the same operations as tools: `run`, `discover`, `findings`,
`attest_survivor`, and `ephemeral`. The ephemeral tool takes a hand-crafted
mutation as exact-match edits (state the change, not the file):

```json
{"file": "lib/parse.go",
 "edits": [{"old": "return dec(v)", "new": "return dec(v[1:])"}],
 "test_pkg": "example.com/lib", "run": "^TestParse$"}
```

An edit that matches nothing or ambiguously is refused, never guessed.
The run and discovery tools accept the same package and symbol filters as the
CLI. Run results include the same ordered cache/measurement decisions,
per-operator dispositions, aggregate summary, changed-scope residue, and
findings-document update semantics. A request carrying an MCP progress token
additionally receives progress notifications for preparation events, target
decisions, and freshness-analysis keep-alives. The `run` and `ephemeral` tools
default `timeout_sec` to 300 seconds when omitted — below typical MCP client
request deadlines — and an explicit 0 means unlimited. MCP runs are
synchronous tool calls. Agents
must use the CLI for work that may exceed their harness's MCP request timeout;
progress delivery does not guarantee that a client extends its wall-clock
limit.

## Library

The CLI is a thin shell over the root package:

```go
tree, _ := gomutant.Load(".")
targets := tree.Discover() // or DiscoverChanged, ParseTargets
findings, _ := tree.Run(ctx, targets, gomutant.Options{Budget: 5, Prior: prior})
doc, _ := gomutant.Export(findings)
```

Targets are producer-agnostic (REQ-target-producers): auto-discovery, a
config document, or an external producer's export all reduce to the same
model — a symbol, its kill oracle, and opaque labels echoed onto findings.
