# Oracle tests' in-module scratch records as manifest noise

**Lands: cross-tool train chunk 37.
gofresh's scratch-namespace admission (REQ-inputs-scratch-namespace)
drops reads proven absent at both observation-bracket endpoints, but
only inside a caller-declared namespace. gomutant's oracle ingest
declares none, so an oracle test minting in-module scratch records
missing-arm identities with random per-run names — sound, but noise
that defeats union-equality across runs (the serve carve-outs'
persisted-union comparisons) exactly as it did for pew benches. pew's
answer is the //pew:scratch directive; gomutant needs an equivalent
declaration surface for oracle packages.

Second field report, requirements input for the declaration surface:

- A declared --bracket-path over a transient per-test directory
  completed a full 100-candidate measurement, then failed with
  "observation bracket unverifiable: unhashable runtime directory" —
  the subdirectory was deleted by test cleanup before hashing. Declared
  bracket paths MUST preflight (existence + hashability) before mutant
  execution, refusing up front instead of burning the campaign.
- The declaration this surface provides must be able to express "this
  in-module path legitimately churns" — a transient scratch namespace,
  not a stable hashable input; until it can, affected targets are
  permanently machine-local.

Third field report (subshuffler campaign, filed in
[oracle-ephemeral-root-undeclared](oracle-ephemeral-root-undeclared.md))
endorses a `--scratch-namespace DIR:PATTERN` flag shape for the
declaration surface; the tool-minted out-of-module TMPDIR half of that
report is the separate one-seam chunk-85 fix, not this surface.

