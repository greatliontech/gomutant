# gomutant overview

gomutant is a mutation tester for Go. It breaks a target symbol's body on
purpose, runs the tests that vouch for that symbol against each mutant, and
reports the mutants no test caught. A survivor is a finding: either the test
is weak and should be strengthened, or the mutant is equivalent and should be
dispositioned. gomutant measures whether tests have teeth; it never decides
whether anything is "covered" — that judgment belongs to whatever consumes
its findings.

## Executable identity

**REQ-cli-version** (behavior): The CLI MUST expose `version` and `--version`
as project-independent queries of the running binary's embedded identity.
Both print the same single line: tool name, module version or development
identity, revision, modified state, compiler and linked Gofresh version or
replacement. Missing facts remain unknown, never inferred from cwd, git, a
selected Go command or the network. The shared toolversion metadata rule is
the source of those facts. Findings write/read format versions remain separately
labelled capability information. The query loads no project or policy, starts
no profiler, installs no execution-memory policy and writes no tool state.
It works with malformed project files and without go or git on PATH. Help
advertises both forms; the subcommand takes no positional operands. Existing
MCP initialization reports the running server's same embedded identity rather
than a constant or another installed binary's identity.

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
4. **Advisory, pinned.** Findings record the measured outcomes and the evidence
   available to reuse them — target and oracle source closures, build and runtime
   guards, observed runtime inputs, operators, budget, and oracle timeout. Only findings
   whose pins prove reusable may be served without measurement; an unverifiable
   finding remains a report of its completed run and remeasures on the next request
   ([results.md](results.md)).

## The keystone

**REQ-core-attributed-kills** (invariant): Every reported kill MUST rest on
an attributed event — a named failing oracle test, a timeout, or a
package-scope failure a baseline probe confirms is real. A run that cannot
attribute an outcome aborts without writing findings, never reporting an
unattributable outcome as a kill. Kill counts are a measurement; a corrupted
measurement that reads as a sound one inflates them in the flattering
direction, so the tool refuses it rather than guess. The attribution is
durable: a measured finding persists each kill's killer beside the kill
(REQ-result-record in [results.md](results.md)), and a record carries either
every kill's attribution or none. Attribution records the deciding event;
it does not prove that the test is independent of other tests or license
retaining its outcome across changed oracle evidence (REQ-result-stale).
