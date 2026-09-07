# Three env-key composition rules where one would do

`GoEnv` strips `GOWORK` case-insensitively on every platform and
appends the pinned value (internal/engine/engine.go); `oracleCPUEnv`
strips `GOMAXPROCS` case-folded on Windows only and appends the cap
(internal/engine/parallelism.go); `oracleMemoryEnv` appends `GOMEMLIMIT`
with no strip at all (internal/engine/memlimit.go). The duplicate-key
hazard the GOMAXPROCS fix addressed — gofresh's producer env refuses a
duplicate key — is a property of the append shape, not of one key: an
ambient `GOMEMLIMIT` would reproduce it the day the memory ceiling's
entry reaches the producer env.

The collapse: one `setEnvKey(env, key, value)` with one documented
key-case rule (the platform's own: fold on Windows, exact elsewhere),
every composer routed through it, pinned by one property test over
arbitrary ambient environments (the key appears exactly once after
composition, with the intended value). `GoEnv`'s every-platform fold is
the one semantic to settle — it predates the Windows-only rule and may
be deliberate for `GOWORK`.

Lands: the next change to an oracle or process env composer (GoEnv, oracleCPUEnv, oracleMemoryEnv).
