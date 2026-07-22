# Ephemeral replacements outside the oracle's import closure measure unexercised

Lands: when the engine's package load gains dependency data
(NeedImports/NeedDeps) so closure membership is computable, or when ephemeral
results gain execution buckets that expose the unexercised case.

## Observed

Ephemeral validation refuses replacements the loaded build does not compile,
but a compiled file in an in-tree package the probed test package never
imports still overlays cleanly: the oracle runs, never links the mutated
package, every test passes, and the result reports `killed: false` — a false
survivor for a mutant the oracle could not have noticed. The load mode
carries no dependency data, so the import-closure check is not computable
without a load-mode change whose cost lands on every tree load.

## Resolution

Either extend the load mode with import data and refuse replacements outside
the probed test package's closure, or bucket the ephemeral result with the
survivor execution evidence (a coverage probe would classify the unexercised
case) so the false-survivor reading is labeled instead of silent.
