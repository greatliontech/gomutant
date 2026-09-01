# gomutant's own chunk gate: suite decomposition is the remaining lever

Chunk 137 landed the ruled option (b) — survivor-oracle narrowing
with a full-oracle audit — plus the campaign economics around it
(upfront cost model, value-ordered windows, deadline-bounded SIGTERM
drain, savings-derived audit cap, the baseline bank). The relaunch
measured the result on gomutant's own face:

- The cost model priced window 1 (5 works, 92 candidates) at
  ~33h43m with 79 of 92 candidates NARROWED — the narrowing engaged
  but bought little, because on an e2e-heavy orchestrator suite
  nearly every test transitively reaches the core paths: covering
  sets approximate the full suite, so a "narrowed" run still prices
  near a full-oracle one (~21 min).
- The live pace extrapolated ~217h (~9 days) for the grown selection
  (129 targets / 10,235 candidates), and the campaign was stopped at
  3h33m on its own verdict — the priced-upfront refusal the chunk
  exists for, versus discovering the same fact days in.
- The audit share priced at ~10.8% of the window — inside the 1/8
  bound — and the first window was value-ordered (a cheap five-work
  window, not the arrival-order giants).

So the narrowing is the right lever on suites with sharp coverage
(unit-style package oracles) and a weak one here. gomutant's own
chunk gates therefore continue to stand on `gomutant ephemeral`
probes with named deciding tests, and the remaining lever for a true
full-face campaign is the fork's other half, explicitly left
unchartered by the 2026-08-31 ruling:

SUITE DECOMPOSITION — split the monolithic root suite so derived
per-group oracles shrink; keeps every verdict's oracle semantics,
costs test-architecture work in each tool repo. With decomposed
suites, the chunk-137 machinery (narrowing, banking, cost model)
multiplies the win instead of fighting the suite shape.

Lands: user decision — chartering suite decomposition is the (a)
half of the campaign-scale fork the user ruled (b) on.
