# A transient compiler crash under a probe reads as a broken mutant

Consumer report (bldc, 2026-09-02): one `ephemeral` run died with a
Go compiler SIGSEGV in `cmd/compile/internal/ir.Visit` during
inlining; the identical probe compiled and measured cleanly on
immediate re-run. The root cause is the Go toolchain, but the probe
surfaced it as a bare failure indistinguishable from a mutant that
does not compile. Ask: retry once on a compiler crash (a signal
death of the compiler process, as opposed to a diagnostic), or mark
the result "compiler crashed — re-run to confirm", so a transient
never reads as a verdict. Reproduction: none stable — one occurrence
in ~30 probes that session.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
