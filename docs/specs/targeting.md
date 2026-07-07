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
schema, and none is privileged. The reference external producer is
stipulator: gomutant parses stipulator's targets export — each entry's
symbol becomes a mutated body, its witness tests become the oracle, and its
requirement identifiers ride as labels — so stipulator owns that wire format
and gomutant owns the adapter, keeping stipulator ignorant of gomutant while
the tool stays complete standalone.

**REQ-target-oracle** (behavior): A target's oracle MUST be the sole arbiter
of a kill: a mutant of the target is killed only when a test in that oracle
fails (or the run times out, or a probe-confirmed package failure occurs per
REQ-core-attributed-kills). A test outside the oracle that happens to fail on
the mutant never counts — the oracle scopes the measurement to the tests that
claim to vouch for the symbol.

**REQ-target-default** (behavior): A target given no oracle MUST fall back to
a derived one — the tests of the symbol's own package — so a bare list of
symbols, or whole-package discovery, is a usable run without a caller
enumerating tests. An explicit oracle overrides the default.

**REQ-target-labels** (behavior): Labels MUST be carried from a target onto
every finding it produces, unmodified and uninterpreted, so a finding can be
grouped by a caller's own vocabulary. gomutant reads no meaning from a label;
a requirement identifier, a subsystem name, or a ticket number are all just
strings it groups and prints by, which is what keeps the tool domain-agnostic
while letting a spec-driven producer recover requirement-scoped findings.
