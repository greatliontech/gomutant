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

# Measure only what changed since a ref, budgeted for the hot loop.
gomutant run -changed HEAD -budget 5

# Explicit targets (symbol + oracle + labels) from a JSON document.
gomutant run -targets targets.json

# Open findings, grouped by label.
gomutant findings

# Disposition a survivor as equivalent, with the reasoning on record.
gomutant attest -symbol example.com/pkg.F -position f.go:10:5 \
    -operator "zero return" -reason "result unused on this path"
```

Findings live in a versioned JSON document (default
`.gomutant/findings.json`), pinned to the inputs that produced them — the
target's body hash, each oracle test's body hash, the operator-set version,
the budget, and the toolchain. A run re-measures exactly what a moved pin
invalidates and serves the rest from the document. Open findings are
survivors minus attested dispositions; whether they fail a build is the
caller's policy, not gomutant's verdict.

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
