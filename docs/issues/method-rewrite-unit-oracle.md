# methodDeclarationRewrite campaign oracle is mis-scoped — 86 machine-local survivors, 0 kills

A `--changed`-scoped campaign over the engine package measured
`Tree.methodDeclarationRewrite` whole (127 candidates, no prior
record) and recorded **0 killed, 86 open survivors** — every mutant
scored survivor by construction, because the campaign's oracle was
the engine package's own tests while the function's teeth live one
package up: the root package's structural-probe suites.
Demonstrated coordinate: `TestMethodProbesRewriteDeclaration`
(`shaped_unit_test.go:102`) kills the receiver-match mutation
`structural.go:235` `!=` → `==` in 4.58s — a mutation the
engine-scoped campaign recorded as an open survivor.

Two evidence-quality notes ride the machine-local record (taken
under the run's standing vouches):

- Every batch-phase "kill" came from the self-measuring engine
  tests (`TestProbeBaseline*`,
  `TestObservedRunScoresAgainstStableRuntimeInputs`, …) and every
  one re-scored survivor on serial full-oracle confirmation —
  noise oracles for this target, not teeth.
- The whole record is runtime-unverifiable (a build-cache path
  escaped the observation bracket; the recorded guidance names the
  unstable test set and a stable-oracle subset to re-run with).

Sampled data point (line 235, the receiver match — six
survivors): three died to the pre-existing root oracle
(skip-direction mutants), three survived it too — the fixture
declared only one method named `Do`, so an inert receiver check
discriminated nothing. The fixture gained a same-named `Decoy`
method ahead of `Impl` and the rewrite test asserts the decoy
survives byte-intact (landed with this filing); all six now die
to the root oracle. The mass is therefore measured as roughly
half measurement artifact, half real gap on the sampled line.

Resolution shape — oracle scoping first, oracle extension second:

1. Campaign oracle selection must reach the root-package suites
   for engine symbols whose coverage lives there (or the run must
   say it cannot, instead of minting survivors an existing test
   kills).
2. Extend the root-package oracle to the arms it demonstrably does
   not pin: the drift-pin refusal (`SourceDriftError` on moved and
   on content-drifted declaring files) and the offset-bounds
   refusal arm. (The third demonstrated arm — receiver-match
   discrimination — is closed by the decoy fixture above.)
3. Stable-oracle re-run per the recorded guidance to retire the
   remainder.

The related scoping work (baseline scale, oracle ordering, scoped
oracles) is already chartered in the same chunk.

Lands: train chunk 113
