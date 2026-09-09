# MCP Campaign Observability And Preflight

A long-running MCP campaign needs enough visible and recoverable state for its
caller to distinguish progress, a slow phase, completion, and cancellation.
The current token-dependent stream and CLI-only preflight leave that distinction
unavailable to an agent whose client does not expose the request metadata or
retain notifications. This is a UX extension request, not a claim that tokenless
silence or the missing MCP preflight violates the current surface contract.

## Consumer Evidence

Stash invoked installed revision `01f733a7cb4f26403344469ae360f0e472239255`
(Go `go1.27.0-X:nodwarf5`, GoFresh `v0.99.0`) with:

```json
{"changed":"9ce8acb","budget":0,"timeout_sec":0,"jobs":4}
```

Run `f6e9f814a3077f67` started on 2026-09-09 at 18:36:58 +03:00. The user
interrupted/restarted the client around 19:56 because no useful progress or final
result was visible. The old process was absent after restart. This is not a
demonstrated deadlock: the machine-local store contains 14 records from the run,
accounting for 376 generated candidates, 305 killed, 42 open, and 29 discarded.
All 42 open entries have the `unstable-oracle` execution bucket; these are not
42 established application defects or reusable proofs.

Local record modification times show progress throughout the interval:

| Local time (+03:00) | Persisted target |
| --- | --- |
| 19:00:25 | `expand.Config.glob` |
| 19:13:57 | `interp.Runner.updateExpandOpts` |
| 19:25:04 | `syntax.Printer.spacedString` |
| 19:40:34 | `syntax.Printer.testExpr` |
| 19:52:45 | `syntax.Printer.indent` |

These are filesystem modification times, not embedded measurement timestamps.
The remaining 31 targets' attempt status and the last active phase are unknown.
The repository findings diff contains only version 11 to 12 and an empty
`coverageBounds` array. Inspecting that file alone initially produced the wrong
diagnosis of no committed work; `findings --run f6e9f814a3077f67` exposed the local
records. Both storage layers must be represented in progress and recovery.

The retained OpenCode tool part has only pending/running events, no progress or
terminal response. Whether its wire request supplied a progress token was not
recoverable. The current MCP log contains only the restarted session's startup
lines; the earlier log was not found. Normal startup is not proven to have
removed it: Gomutant opens the log in append mode and rotates one generation.

The same selection's non-measuring CLI preflight subsequently reported:

```sh
gomutant run --dir /path/to/stash --changed 9ce8acb --budget 0 --jobs 4 --plan --timeout 5m
```

```text
plan 45 measure (2441 candidates), 0 cached, 0 skipped
```

Individual targets use up to 399 tests across 11 packages. The plan exposes that
fanout and candidate count, but does not run baselines or supply a runtime ETA.
Its progress line still says targets committed and candidates 0/0 during
preflight, even though preflight intentionally commits and executes nothing.

## Current Mechanism And Requested Outcome

At `internal/mcpserver/server.go`, `progressNotifier` returns nil without
`params._meta.progressToken`. That envelope metadata is not a `run` argument.
There is then no heartbeat or campaign-progress logging fallback; preparation
and decisions wait for the eventual capped response, while execution events are
not retained by `runStreams.executing`. The file log is a lifecycle/protocol log,
not a campaign progress archive. `runIn` has no plan option, as documented by
`REQ-mcp-surfaces`; `discover` is not equivalent to `run --plan`.

Provide an actionable and recoverable long-run workflow for this caller shape:

- make the non-measuring preflight reachable or explicitly direct the agent to
  the CLI before an exhaustive request; do not mistake `budget: 0` for a dry run;
- expose whether live progress is actually deliverable, rather than promising
  a heartbeat the caller's envelope did not request;
- make run identity, active work, committed counts in both layers, and final or
  interrupted disposition recoverable without relying solely on a lost reply;
- distinguish preflight activity from execution/commit counters in its renderer;
- preserve locality and reuse-refusal distinctions, bounded output, conservative
  evidence admission, and the fact that a heartbeat alone does not prove work.

The implementation choice belongs with the owning face-parity work. No particular
new status service, transport extension, or evidence relaxation is prescribed.
The related `mcp-liveness-cancellation-witness.md` concerns transport lifecycle;
it does not provide the missing campaign observability or preflight surface.

Lands: cross-tool train chunk 207
