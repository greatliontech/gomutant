# Killed mutants leave oracle scratch residue with mutant-mangled state

Lands: cross-tool train chunk 41 (the consumer-surface audit carries
the consumer-hygiene paragraph: scratch helpers enforce their own
freshness and treat permission-mangled residue as expected).

## Observed (2026-08-14, ocifs)

Mutant oracle processes the campaign kills (timeouts, losers) never
run their `t.Cleanup`, so whatever scratch state the oracle created
survives — and because the process was running *mutated* code, the
residue can be shapes the clean suite never produces. Two concrete
hits in one consumer:

- A later property run failed spuriously (`mkdir
  .scratch/export/1/root: file exists`): the consumer's scratch
  helper assumed per-process freshness (`MkdirAll`), and a killed
  mutant's residue occupied the sequence-numbered directory. Fixed
  consumer-side with RemoveAll-before-MkdirAll.
- Residue directories carried mutant-mangled permission bits (a
  chmod-application mutant), making them undeletable by plain
  `rm -rf` until a manual `chmod -R u+rwx` — a trap for any sweeper,
  including gomutant's own scratch machinery if it ever inherits
  such a tree.

Neither is a gomutant defect — kill-without-cleanup is inherent to
mutation testing — but the failure shape (clean suite breaks *after*
a campaign, in a directory the campaign never touched directly) cost
a diagnosis round, and the hardening rule is general: consumer
scratch helpers must enforce freshness themselves, treating
permission-mangled residue as expected. Worth a paragraph in the
oracle guidance so the next consumer reads it instead of
rediscovering it.
