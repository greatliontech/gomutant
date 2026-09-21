# stipulator check red on three requirements (fleet sweep 2026-09-21)

The weekly fleet sweep's `stipulator check` over this repo fails for
the first time since the sweep began (every earlier sweep reported
`pass` with uncovered rows only):

- violation: REQ-exec-go-command-runner is red and no gap excuses it
- violation: REQ-exec-spawn-environment is red and no gap excuses it
- violation: REQ-target-selection is red and no gap excuses it

The rows file behind the report carries no stale-pin or broken-witness
row for the three — only the violation lines — so the check's summary
view does not say which binding turned each red. Undiagnosed. The
likely proximate is in the reflog: between the 2026-09-14 sweep (pass)
and this one, chunk 278's change sets moved exactly the code those
requirements bind — "one key rule for every composed spawn
environment; the loader's driver pinned off", "the go-tool arms onto
gofresh — the sampler, the env snapshot, the listing, the load's
coordinate", "the standing vouch set's one home — the tree root's
file, read at the load", and "one target-source dispatch for both
faces". Bindings pinned on the deleted copies would go red under the
runner, environment, and target-selection requirements precisely. But
a re-bind is per requirement with the bound bodies read, never a
blanket, and any row that stays red after re-binding is a genuine
regression — the diagnosis is the first step, not assumed.

The two `uncovered` rows beside the reds (REQ-result-export, +8;
REQ-target-model, +1 — bound witnesses classified example-grade)
predate the red and rode every passing sweep; they are context, not
this doc's claim.

Scanned: the index and all 49 docs — none tracks this repo's own
check verdict; mcpserver-witnesses-unbound (chunk 219) and
enumeration-lag-gate-unstated (chunk 209) are binding gaps on other
requirements, disjoint from these three rows.

Lands: cross-tool train chunk 278 — the chunk whose change sets moved
the bound bodies; its close-out re-binds the runner, environment, and
target-selection requirements onto gofresh's forms (the train's
standing rule keeps the self-check off the chunk loop, so the verdict
itself is re-taken at the next named point — the release push that
follows the bump), and whatever stays red is diagnosed there.
