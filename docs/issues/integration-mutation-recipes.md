# No recipe-shaped mutation classes for generator and seam adequacy

Lands: when a caller repeatedly needs manual mutants for generated data,
resolver seams, or caller mappings.

The operator set covers expression-level body changes. Some recurring
adequacy checks are recipe-shaped instead:

- mutate one generated value while leaving the source input unchanged,
  proving a drift guard notices;
- drop or invert a caller-side fail-closed mapping, proving the
  integration seam surfaces the intended unresolved state;
- remove a parser guard for a required input, proving malformed input
  fails closed;
- change a resolver precedence edge, proving the composed result selects
  the legally stronger or more specific source.

These are not arbitrary fuzzing: they are named mutation classes over
repo shapes the target set can identify — generated output paired with
source input, parser guards paired with diagnostics tests, resolver and
caller seams paired with behavior oracles. Recipes arrive as part of the
target set (targeting is an input, never a discovery), report survivors
as ordinary findings, and carry the caller's opaque label saying what the
recipe was intended to attack. Provenance: migrated from stipulator
(`git log --all -- docs/issues/harden-integration-recipes.md` there)
when mutation ownership moved to gomutant.
