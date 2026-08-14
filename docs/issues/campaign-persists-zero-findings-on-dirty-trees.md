# Campaigns on dirty trees complete but persist zero findings

Lands: cross-tool train chunk 38a (the staged index-snapshot line and
its CLI/MCP surfaces are landed; the chunk verifies persistence and
later-run reuse against a pinned pre-commit consumer loop, diagnoses
the zero machine-local reuse, makes non-persistence loud, and grows
the version surface this report could not capture).

## Observed (2026-08-14, ocifs, installed binary at ~/.local/bin/gomutant)

Every mutation campaign in the ocifs repo runs on a dirty tree by
protocol — the consumer's adversarial loop stages a change set
(`git add -A`) and measures before committing. Across four campaigns
that day (`gomutant run --changed HEAD --timeout 0` with seven
`--bracket-path` args; summaries 628/226, 342/177 twice, 667/413
generated/killed), every run completed normally (exit 0) and every
`measured` line reported real candidate/mutant/kill counts, yet
`.gomutant/findings.json` afterwards held `{"version": 7,
"findings": []}` — 30 bytes. Each per-target block carried a
`machine-local: dirty worktree provenance` note.

The ocifs document's own git history shows this is not a one-day
regression: the committed document is empty at every version it ever
carried (v5 at ocifs f722b5b, v6 at 81c61af, v7 at 2abea4d) — the
consumer has never once had a finding persist, across what appear to
be several binary generations.

Consumer-visible cost: zero incremental reuse (every campaign
re-measures everything, `0 cached` in every summary), and survivor
detail exists only in the run's stdout — one campaign's survivor
list was permanently lost when the consumer piped output through
`tail -25`, forcing a full re-run.

## Candidates

- Dirty-provenance findings deliberately not merged into the
  document (the schema carries a `dirty` field, so persistence with
  the flag looks intended — if refusal-to-persist is the design, the
  summary should say so instead of ending silently with an
  unchanged document).
- The final merge path dropping all findings whose provenance is
  machine-local, rather than only declining reuse for them.
- The installed binary predating whatever currently governs this;
  version output was not captured (the binary answers neither
  `--version` nor `version`), which is its own small gap for field
  reports like this one.

## Reproduction

ocifs @ 3c70fe5: stage any small delta, run the repo's `task mutate`,
inspect `.gomutant/findings.json` before and after.
