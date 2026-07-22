# Targeting

A mutation run cannot begin without knowing what to mutate and what decides a
kill. That knowledge is the target set — gomutant's primary input. The design
principle is that this input is *the same* however it is produced: gomutant
owns one targeting model, and every source fills it.

**target** (term): one symbol to mutate, paired with its kill oracle and its
labels. The unit of a mutation run.

**oracle** (term): the set of tests whose failure counts as catching a
mutant of a target. A mutant survives exactly when no oracle test fails.

**label** (term): an opaque string carried on a target and echoed, unchanged,
into the finding it produces. gomutant assigns labels no meaning.

## The model

**REQ-target-model** (structural): A run MUST be driven by a target set in
which each target names a symbol to mutate, an oracle (zero or more test
symbols), and zero or more labels. Mutation operators, the per-symbol budget,
and execution limits are run-wide configuration, not per-target state — a
target says *what* to break and *what catches it*, never *how* to break it.

**REQ-target-producers** (behavior): gomutant MUST reduce every source of
targets to one internal model — auto-discovery, a config file, and an
external producer's document are parsed onto the same target set, never three
code paths downstream of the parse. A producer emits its own format and
gomutant parses it; no producer is required to speak a gomutant-defined
schema, and none is privileged. Stipulator is the reference external producer:
it owns its binding-surface wire format while gomutant owns the strict adapter
from that format, keeping the producer ignorant of mutation semantics while
gomutant stays complete standalone.

The gomutant-owned config-file encoding is one valid UTF-8 JSON object with a required,
non-null `targets` array. Each array entry is a non-null object with required
non-null string `symbol` and optional non-null `oracle` string array, `labels`
string array, and boolean `oracleExplicit`. Field spelling is exact and
case-sensitive. Unknown or duplicate fields at
either object level, null structural fields, and trailing JSON are malformed
and MUST be rejected rather than silently changing the target or oracle.

**REQ-target-stipulator** (behavior): The Stipulator adapter MUST use the
version-matched Stipulator binding-surface Go wire module to parse and validate
`stipulator.binding-surfaces/v1` reports before reducing compatible surfaces to
the ordinary target model. Any format, ProtoJSON, required-field, ordering,
uniqueness, value-domain, or identifier error from the producer-owned module
refuses the whole document rather than being repaired or partially mapped. A
valid empty report maps to an empty target set.

A compatible surface and every one of its bindings use backend `go`; an
incompatible surface refuses the document with its identifier, backend, and
symbol rather than being silently omitted. Its implementation symbol becomes
the target symbol. The distinct sorted symbols of both binding roles become
the explicit oracle, including an explicitly empty oracle. Its sorted labels
are the raw requirement identifiers plus
`stipulator:surface:<surface-id>`; labels form a distinct set sorted
lexicographically over UTF-8 bytes. The producer document supplies no build
flags, race mode, operators, budgets, timeouts, or other run-wide policy.

**REQ-target-oracle** (behavior): A target's oracle MUST be the sole arbiter
of a kill: a mutant of the target is killed only when a test in that oracle
fails (or the run times out, or a probe-confirmed package failure occurs per
REQ-core-attributed-kills). A test outside the oracle that happens to fail on
the mutant never counts — the oracle scopes the measurement to the tests that
claim to vouch for the symbol.

An oracle is accepted only when every named test maps to one uniquely selectable
and attributable event in the Go test binary. When in-package and external-test
variants declare the same displayed top-level name, the Go backend rejects that
oracle as ambiguous rather than deduplicating the declarations or guessing which
variant produced an event.

**REQ-target-default** (behavior): A target given no oracle MUST fall back to
a derived one — the runnable tests of the symbol's own package: its Test
functions and the seed-corpus runs of its Fuzz targets, both variants, and
nothing an ordinary `go test` invocation would not execute (a helper whose
name merely starts with Test, or the TestMain harness, can kill nothing, so
admitting it would derive an oracle that executes nothing and every mutant
would survive an empty run) — so a bare list of symbols, or whole-package
discovery, is a usable run without a caller enumerating tests. An explicit
oracle overrides the default — including an explicitly *empty* one: a
producer whose document is a complete statement of who vouches (stipulator's
export) marks its oracles explicit, and an unwitnessed target then reports
as measurable by nothing rather than inheriting package tests it never
claimed, which would launder unbound kills into the producer's labels.

**REQ-target-changed** (behavior): Auto-discovery MUST offer a changed-scope
mode that targets only the symbols whose bodies differ from a caller-named
git ref — compared by canonical body hash per declaration, so a one-function
edit in a thirty-function file yields one target, formatting churn yields
none (whitespace inside string, rune, and raw literals is content, never
churn: the canonical projection preserves literal interiors byte-exact), a declaration absent at the ref (a new file or a new symbol) reads as
changed, a symbol deleted since the ref yields no target (nothing remains to
mutate), and an unparseable prior version conservatively reads as all
changed. Test sources are oracles, never targets, and are excluded from the
changed surface. The mode also reports the changed-but-untargeted
residue with the engine-level reason each path yielded no target — a test
file, a generated file, a non-Go or data-only file, a changed file declaring
no function body, a file whose declared bodies are all canonically unchanged
(formatting-only churn), a file whose only change is a deleted symbol, or a
Go file the loaded packages do not cover (deleted, unparseable, or excluded
by build constraints — an unbound surface named as such, never mislabeled) —
so a caller layering its own classification (or a user deciding what to
hand-mutate) sees the whole changed surface, never a silently narrowed one.
The one named exclusion: paths under gomutant's own state directory
(`.gomutant/`) are outside the changed source surface and report as neither
targets nor residue — the tool's bookkeeping can never produce a mutation
target, and reporting the tool's own writes back as residue would put
self-noise in every incremental run over a tree the tool has measured. A
caller relocating tool artifacts outside that directory opts back into
ordinary classification.
This is what keeps an incremental run proportional to the edit rather than
to the tree.

**REQ-target-labels** (behavior): Labels MUST be carried from a target onto
every finding it produces, unmodified and uninterpreted, so a finding can be
grouped by a caller's own vocabulary. gomutant reads no meaning from a label;
a requirement identifier, a subsystem name, or a ticket number are all just
strings it groups and prints by, which is what keeps the tool domain-agnostic
while letting a spec-driven producer recover requirement-scoped findings. A
caller correlating Stipulator results accepts a finding for a current binding
relationship only when its labels contain that relationship's current
`stipulator:surface:<surface-id>` value.

**REQ-target-inspection** (behavior): Target inspection MUST render the exact
effective target set a run would consume without running mutants: each symbol,
its sorted oracle identified as explicit or package-derived, its sorted opaque
labels, and changed-scope residue with reasons. Duplicate symbols and invalid
or ambiguous oracles are refused exactly as a run refuses them. Human and
machine-readable CLI views and MCP discovery derive from the same target
descriptions, so inspection cannot disagree with execution.

**REQ-target-filtering** (behavior): Run and target inspection MUST accept
repeatable package and symbol filters over every target producer. Filters use
the complete-input pattern language of `github.com/greatliontech/glob`:
package patterns match the target's Go import path and symbol patterns match
its fully qualified symbol. A target matches when it matches at least one
pattern of each supplied kind; omitting a kind imposes no constraint. An
invalid pattern or a supplied filter set selecting no targets is refused
rather than treated as a successful empty run. Filtering is scoped: it says
which existing targets to inspect or measure, never that unselected symbols
ceased to exist.
