# stipulator adopts gomutant (and gofresh) in one pass

Lands: with stipulator's adoption pass — the effort that swaps stipulator's
hardening engine for this library and brings gofresh-backed freshness to its
witness records.

## Context

The extraction is complete on this side: the engine, targeting, records,
CLI, and the stipulator seam (Tree.Fresh, ParseStipulatorTargets) all
landed. The stipulator side was deliberately deferred because it entangles
with stipulator's separately planned gofresh adoption — its records layer,
gate reminder, and MCP surface would otherwise be reworked twice.

## The stipulator-side work (scoped during extraction)

- Targets export: a stipulator verb emitting its version-tagged document
  ({"stipulatorTargets":1, targets:[{symbol, witnesses, requirements}]}) —
  stipulator owns the format; the adapter here already parses it.
- harden verb: Plan (bindings → targets) stays; Run/Records swap to
  gomutant.Run with witnesses as oracles and requirement ids as labels;
  kill-sheet store adopts the finding document (stipulator hardening.md
  REQ-harden-records amendment — pins are identical).
- Gate reminder rewires to Tree.Fresh; attest_survivor to Finding.Attest;
  --ephemeral to Tree.Ephemeral; --staged-diff keeps stipulator's
  binding-aware classification over its own Surface (which stays).
- Delete from backends/golang/harden.go: Mutants + operators, RunMutant,
  TestProbe, SplitRapidPkgs, BodyHash, fileOf, renderFile. Keep: Vacuous
  and its helpers (bind-time policy, verify.VacuityChecker) and Surface.
- gomutant needs a remote (or a local replace) before stipulator's go.mod
  can take the dependency.

## Resolution

On landing: implement in stipulator under its own plan, promote this
scoping there, delete this doc — git holds history.
