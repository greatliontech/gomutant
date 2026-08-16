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
code paths downstream of the parse. An external producer emits the
gomutant-owned config-file document below — one schema, one parse, no
producer privileged over another — keeping producers ignorant of mutation
semantics while gomutant stays complete standalone.

The gomutant-owned config-file encoding is one valid UTF-8 JSON object with a required,
non-null `targets` array. Each array entry is a non-null object with required
non-null string `symbol` and optional non-null `oracle` string array, `labels`
string array, and boolean `oracleExplicit`. Field spelling is exact and
case-sensitive. Unknown or duplicate fields at
either object level, null structural fields, and trailing JSON are malformed
and rejected rather than silently changing the target or oracle.

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
producer whose document is a complete statement of who vouches marks its
oracles explicit, and an unwitnessed target then reports
as measurable by nothing rather than inheriting package tests it never
claimed, which would launder unbound kills into the producer's labels.
A derived oracle is a measurement pin only if it is provably fresh: the
package loader's snapshot has been observed lagging the filesystem under
rapid successive invocations, and a lagging enumeration is a silent coverage
cap recorded as evidence — new tests absent from the derived set while the
same run's other evidence reads the current tree. Before a run trusts a
derived set, it cross-checks the enumeration against a direct parse of
the package's on-disk test files — every on-disk test file the effective
build configuration selects, the configuration resolved from the tree's own
environment (a persisted go-env value or a GOFLAGS tag changes which files
the test binary compiles), snapshot-present files re-matched exactly like
new ones (a constraint edit after the load is itself a lag) — under the
same runnable-test shape, and refuse the run on any disagreement,
naming the differing test identities in both directions. A derived set that
shrank relative to a prior finding's recorded oracle is named loudly in the
re-measure reason: a test the record was measured against no longer exists.

**REQ-target-changed** (behavior): Auto-discovery MUST offer a changed-scope
mode that targets only the symbols whose bodies differ from a caller-named
git ref — compared by canonical body hash per declaration, so a one-function
edit in a thirty-function file yields one target, formatting churn yields
none (whitespace inside string, rune, and raw literals is content, never
churn: the canonical projection preserves literal interiors byte-exact), a declaration absent at the ref (a new file or a new symbol) reads as
changed, a symbol deleted since the ref yields no target (nothing remains to
mutate), and an unparseable prior version conservatively reads as all
changed. Test sources are oracles, never targets, and are excluded from the
changed surface. A `func init()` declaration is an addressable target under
its positional identity `<pkg>.init#<file>#<ordinal>`: `<file>` is the
declaring file's on-disk base name — the unadjusted position, which a
`//line` directive cannot remap — and `<ordinal>` counts the file's receiverless
init declarations in declaration order, 0-based — the declaration-ledger
identity the freshness producer shares, file-scoped so inits elsewhere in
the package never shift it. The ordinal is the suffix after the LAST `#`
(a file base name may itself contain `#`), spelled as canonical decimal.
Discovery emits changed and whole-tree init bodies exactly like named
symbols — a removed init is a deleted symbol — and the derived oracle is
the package suite like any target's, which is also the ground truth of the
oracle relationship: every test of the package executes every init. The
bare name `<pkg>.init` never resolves — the language keeps the identifier
unreferencable — and its refusal points at the positional grammar; an
out-of-range ordinal refuses naming the file's actual init count; a
test-file init is oracle-side source and refuses as such. The mode
also reports the changed-but-untargeted
residue with the engine-level reason each path yielded no target — a test
file, a generated file, a non-Go or data-only file, a changed file declaring
no function body, a file whose declared bodies are all canonically unchanged
(formatting-only churn), a file whose only change is a deleted symbol, or a
Go file the loaded packages do not cover (deleted, unparseable, or excluded
by build constraints — an unbound surface named as such, never mislabeled) —
so a caller layering its own classification (or a user deciding what to
hand-mutate) sees the whole changed surface, never a silently narrowed one.
A test-file residue row additionally names what the changed tests closed
over, best-effort: when prior findings outside the run's target set are
stale for an oracle-caused reason, the row counts them and suggests the
re-measure by symbol — changed-scope discovery alone would never re-measure
them. The count is attribution-free: any oracle-caused staleness qualifies,
including staleness predating the delta, because the cost of naming a
record that already wanted re-measuring is nil and per-file attribution is
not.
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
while letting a spec-driven producer recover findings in its own vocabulary.

**REQ-target-inspection** (behavior): Target inspection MUST render the exact
effective target set a run would consume without running mutants: each symbol,
its sorted oracle identified as explicit or package-derived, its sorted opaque
labels, and changed-scope residue with reasons. Duplicate symbols and invalid
or ambiguous oracles are refused exactly as a run refuses them. Human and
machine-readable CLI views and MCP discovery derive from the same target
descriptions, so inspection cannot disagree with execution.

**REQ-target-selection** (behavior): A run MAY declare a build selection
— build tags and a GOTOOLCHAIN directive — and the declared selection
MUST rewrite the run's one frozen environment before anything reads it,
so package loading, target discovery, build-constraint matching, oracle
spawns, and the measurement pins all observe the same selection: a
`//go:build`-gated symbol or oracle under the declared tags is loaded,
discoverable, and runnable exactly as an untagged one. One run carries
one selection — two selections are two runs, the single-selection
construction gomutant keeps deliberately (the multi-view resolution
form is stipulator's) — and every tree-consuming surface (measuring,
inspection, attestation, lifecycle) accepts the same declaration, so a
tag-gated finding's whole lifecycle happens under the selection that
measured it. Declared tags replace any ambient GOFLAGS `-tags` rather
than merging — a silent union would measure a selection nobody named —
the declared set is order-canonicalized (tag order is presentation,
never a distinct selection), and a malformed tag or directive refuses
before any load, identically whether any selection-keyed cache is cold
or warm. The
toolchain and build-configuration measurement pins carry the effective
selection, so runs under different selections re-measure rather than
serve across each other: the alternation cost is re-execution, stated,
never a served cross-selection verdict.

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
