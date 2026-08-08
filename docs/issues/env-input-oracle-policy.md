# Deterministic module-root discovery is classified as an unstable env input

Lands: cross-tool train chunk 38.
## Observed

A consuming corpus (`candosa/cerebro`) used one repo-root idiom in roughly ten
test files: `os.Getwd()` walked upward to the directory containing `go.mod`.
Under `go test` this is deterministic — the working directory is always the
package directory — but the classification flags the test as consuming a
process-local environment input (`PWD`), bucketing every open candidate on the
affected symbols as unstable-oracle: survivor records become non-committable
(machine-local overlay only), and survivor attestations cannot reach the
repository findings document. The caller resolved it by migrating the idiom to
`runtime.Caller`, which dissolves the flag.

## Design direction (settled this session)

The conservative classification stands. Refining it — "a working-directory
read whose derived value is used only after an upward walk terminating at a
module-root witness is stable" — is a dataflow proof obligation (all uses of
the derived value, through arbitrary helpers), and a mistake's failure
direction is unsound reuse; the wrapper caveat (`go test -exec`, IDE runners
launching from elsewhere) is real. The sanctioned path is the second shape:
a per-repo reviewed exemption record — a committed document naming test
subjects exempt from the env-input instability rule, each exemption carrying
its reasoning, consumed at oracle-stability classification so the affected
records become committable, with the exemption stamped into every finding it
touches. That mirrors the on-the-record discipline `attest_survivor` already
applies to equivalence: auditable and revocable, never a silent global switch.
