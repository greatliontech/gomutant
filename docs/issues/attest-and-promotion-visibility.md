# Attest is silent and promotion trails a commit — layer state is invisible at the moments it matters

Field report (agent consumer), two halves of one visibility gap:

- `gomutant attest` (CLI) succeeds silently — exit 0, no output. It
  should echo the disposition, the remaining open count, and crucially
  the layer: "recorded machine-local (dirty worktree provenance);
  promotes with a clean serve." That single line would have surfaced
  the attestation-stranding defect months before the field diagnosis
  did.
- The promoted document trails by one commit: attest dirty → commit →
  the next run's serve promotes into `.gomutant/findings.json` — which
  is now modified and needs its own commit. Once
  staged-snapshot-run-mode lands this collapses; until then the run
  summary should warn "N records promoted — findings document changed,
  commit it."

Lands: cross-tool train chunk 30 (gomutant consumer-surface bounds and
visibility).

Third moment of the same gap (second field report): attesting against an
already-moved oracle succeeds silently — the test files had been edited
(oracle closure moved) before re-measuring, attest_survivor accepted the
judgment against the old pins, and the next run's pin comparison shed it
silently; the judgment had to be re-made. Shedding is correct by design;
accepting a birth-stale attestation without warning is the gap — attest
has the tree in hand to compare pins and warn (or require a flag).
Repro: measure, touch the test file, attest a survivor, re-run.

