# Spec corpus does not compile diagnostic-free

Lands: when `stipulator compile` over `docs/specs` reports zero diagnostics
under the gating Stipulator version.

## Observed

On a clean tree, `stipulator compile` (locally installed CLI, 2026-07-18)
emits seven diagnostics, so requirement-coverage gating over the corpus cannot
run clean:

- `docs/specs/execution.md:45`: REQ-exec-observation has 3 normative keyword
  occurrences, want exactly 1.
- "normative keyword MUST outside requirement text" at
  `docs/specs/execution.md:140` (REQ-exec-run-status continuation paragraph),
  `docs/specs/mutation.md:39` (INV-MUT-COMPREHENSIVE body),
  `docs/specs/results.md:50` (INV-RESULT-CANDIDATE-CONSERVATION body, three
  occurrences), and `docs/specs/targeting.md:35` (REQ-target-producers
  continuation paragraph).

The pattern suggests the compiler binds a requirement's normative text to its
first paragraph and does not recognize `INV-*` blocks as requirement carriers,
while the corpus writes multi-paragraph requirements and normative invariant
blocks. The gating Stipulator version could not be determined from the repo
(`.stipulator/manifest.textproto` carries only the include glob), so whether
this is corpus drift or compiler-version drift is undetermined — either way the
corpus and the compile gate currently disagree.

## Resolution

Pin or determine the gating Stipulator version, then reconcile: reword each
flagged block to one normative keyword within recognized requirement text, or
adopt a compiler version whose grammar admits the corpus's multi-paragraph and
`INV-*` forms; re-run the compile and coverage gates to zero diagnostics.
