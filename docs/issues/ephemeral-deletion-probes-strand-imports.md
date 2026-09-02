# A deletion probe strands its error path's imports, forcing neutering instead of deletion

Consumer report (bldc, first met 2026-08-29, recurring): an
`ephemeral` probe that deletes a guard whole often leaves the guard's
error path's imports unused — "mutant did not compile: fmt imported
and not used" — so the honest probe (delete the guard) cannot be
written, and the author falls back to `&& false`-style neutering or
to `_ = fmt.Sprintf` padding, which is a different mutant from the
one the question asks about. Met with a fmt-only guard in a mapping
pass and with a finding-package import in a pass harness, and again
during the LSP adapter's review (an errors-only guard).

Ask, either sufficient: run an imports fix over the mutant before
compiling (the probe declares no import intent, so pruning unused
imports cannot change its meaning), or tolerate unused imports for
ephemeral mutants specifically (a probe is never landed code). The
result should say which happened, so a probe that survived only
because an import was pruned reads honestly.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
