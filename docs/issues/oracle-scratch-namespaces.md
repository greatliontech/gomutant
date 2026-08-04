# Oracle tests' in-module scratch records as manifest noise

**Lands:** with the runtimeinput producer facade (gofresh
docs/issues/runtimeinput-producer-facade.md) — the facade is where the
declaration surface for all three consumers collapses.

gofresh's scratch-namespace admission (REQ-inputs-scratch-namespace)
drops reads proven absent at both observation-bracket endpoints, but
only inside a caller-declared namespace. gomutant's oracle ingest
declares none, so an oracle test minting in-module scratch records
missing-arm identities with random per-run names — sound, but noise
that defeats union-equality across runs (the serve carve-outs'
persisted-union comparisons) exactly as it did for pew benches. pew's
answer is the //pew:scratch directive; gomutant needs an equivalent
declaration surface for oracle packages.
