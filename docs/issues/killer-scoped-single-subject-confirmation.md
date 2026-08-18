# Killer-scoped single-subject confirmation

## Symptom

On a consumer whose oracles boot real stores (tugboat), serial kill
confirmation dominates campaign wall time: each confirmation re-runs
the kill's FULL oracle under `-failfast`, so a killer that sorts
late alphabetically pays the whole heavy prefix (~100s observed per
confirmation, machine otherwise idle), and the stride gate that
should sample it away never arms — the window evidence is
unverifiable, because the consumer's own `sync.Pool`s and memos are
admissible only under the single-subject attestation
(REQ-closure-shared-dynamic-state's attestation-gated sets), which a
multi-test oracle process cannot carry. Result: every kill of every
window fully confirms serially; an hour-class changed-scope campaign
becomes day-class.

## Proposal

Confirm a kill by running THE KILLING TEST alone in its own process:

- the serial run is still "alone after the pool drains"
  (REQ-exec-attribution's isolation shape), and a killer-only
  failure is the requirement's first attributed event exactly;
- a one-test process satisfies the single-subject execution
  attestation, so the engine's attestation-gated discharges apply
  and the confirmation's observation can land VERIFIABLE — arming
  the stride gate on precisely the consumers that need it most;
- a flip (the killer passing solo) must NOT score survivor from the
  killer-only run: fall back to the full serial oracle, which keeps
  the anti-flattering direction intact — a survivor verdict always
  rests on the whole oracle.

Open design questions: the scored observation's pin scope (a
killer-only observation is narrower than a full-oracle one; for a
KILL the pin need only cover the killing test's closure — state it
in REQ-exec-attribution rather than inherit it silently), and
whether the measure phase's parallel evidence needs any
reconciliation with a narrower confirmed observation.

Lands: author's triage.
