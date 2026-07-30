# Deterministic module-root discovery is classified as an unstable env input

Lands: either the classification recognizes working-directory reads that resolve
only the module root as stable under `go test`, or a per-repo oracle policy admits
a named-test exemption with its reasoning on record, attestation-style.

## Observed

A consuming corpus (`candosa/cerebro`) uses one repo-root idiom in roughly ten test
files: `os.Getwd()` walked upward to the directory containing `go.mod`. Under
`go test` this is deterministic — the working directory is always the package
directory, and the walk resolves the same module root from every package. The
classification flags the test as consuming a process-local environment input
(`PWD`), which buckets every open candidate on the affected symbols as
unstable-oracle: their survivor records become non-committable and land only in the
machine-local overlay. A survivor attestation made through `attest_survivor` on such
a symbol was accepted but could not reach the repository findings document, and
nineteen sibling survivors on the same symbol read as unverifiable rather than as
the executed-and-passed findings they are.

The caller's fix is migrating the idiom to a source-anchored root
(`runtime.Caller`), which dissolves the flag — but the classification itself is the
conservative reading of a read that is provably stable for the only execution mode
the oracle runs under. The conservatism is defensible: a wrapper (`go test -exec`,
an IDE runner) can launch the binary from elsewhere. What is missing is a sanctioned
way to keep committable evidence when a repository has judged the read stable.

## Shape

Two admissible resolutions, either sufficient:

1. **Refined classification.** A working-directory read whose derived value is used
   only after an upward walk terminating at a `go.mod` (or an equivalent
   module-root witness) is stable per module under `go test`; classify it stable
   and record the reasoning in the finding.
2. **Per-repo oracle policy.** A committed policy document naming tests exempt from
   the env-input rule, each exemption carrying its reasoning — the same
   on-the-record discipline `attest_survivor` already applies to equivalence, so an
   exemption is auditable and revocable rather than a silent global switch.
