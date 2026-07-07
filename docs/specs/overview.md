# gomutant overview

gomutant is a mutation tester for Go. It breaks a target symbol's body on
purpose, runs the tests that vouch for that symbol against each mutant, and
reports the mutants no test caught. A survivor is a finding: either the test
is weak and should be strengthened, or the mutant is equivalent and should be
dispositioned. gomutant measures whether tests have teeth; it never decides
whether anything is "covered" — that judgment belongs to whatever consumes
its findings.

## Stance

Non-normative orientation; each property is specified in its own document.

1. **Targeting is an input, not a discovery.** Every run is driven by a
   target set — symbols to mutate, each with the tests that decide a kill.
   That set may come from auto-discovery, a config file, or an external
   producer; gomutant treats them identically ([targeting.md](targeting.md)).
2. **Domain-agnostic.** gomutant knows symbols and tests, never requirements
   or specs. A caller's vocabulary rides as opaque labels ([targeting.md](targeting.md)).
3. **Mutate, run, attribute.** A symbol's body is mutated through build
   overlays in isolation; its oracle tests run per mutant; a kill is
   attributed to a named event ([mutation.md](mutation.md), [execution.md](execution.md)).
4. **Advisory, pinned.** Findings are records pinned to the exact inputs that
   produced them — body, tests, operators, toolchain — so a record re-stales
   the moment any input moves ([results.md](results.md)).

## The keystone

**REQ-core-attributed-kills** (invariant): Every reported kill MUST rest on
an attributed event — a named failing oracle test, a timeout, or a
package-scope failure a baseline probe confirms is real. A run that cannot
attribute an outcome aborts without writing findings, never reporting an
unattributable outcome as a kill. Kill counts are a measurement; a corrupted
measurement that reads as a sound one inflates them in the flattering
direction, so the tool refuses it rather than guess.
