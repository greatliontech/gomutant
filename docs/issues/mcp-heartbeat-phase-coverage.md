# MCP Heartbeat Phase Coverage And Accuracy

For requests carrying a progress token, `REQ-mcp-envelope` promises a heartbeat
naming the current phase and elapsed time on the shared cadence. Two independent
arms of that promise are not enforced across the complete run handler.

These source paths were independently audited at
`0f955e316eb83090b2d24f030844aecd9fcd41d1` and are unchanged from installed consumer
revision `01f733a7cb4f26403344469ae360f0e472239255`. They establish reachable reporting
faults, not that the Stash campaign stalled in either phase. Tokenless behavior is
the separate UX request in `mcp-run-observability.md`.

## Uncovered Stretches

`loadTreeReporting` wraps loading (`internal/mcpserver/server.go:326-330`), and
`withHeartbeatLabel` later wraps only `tree.Run` (`server.go:1217-1219`). Selection
and oracle-closure signposting between those wrappers, plus final merge/rendering
afterwards, have no periodic heartbeat (`server.go:1045-1053,1236` onward).

The signpost path is concrete: changed test-file residue, prior findings, and an
untargeted prior finding qualify `OracleClosureSignpostContext`
(`gomutant.go:514-536`). It emits an initial notification and then performs
`inspectFindings` without a surrounding heartbeat (`gomutant.go:542-545`). An
inspection lasting longer than the cadence can therefore go silent despite the
request's token. Not every ordinary selection necessarily takes that long.

## Incorrect Current Phase

`runStreams.executing` updates `lastPhase` for probing, executing, and estimate,
but not confirming (`internal/mcpserver/server.go:609-627`). The driver emits
`Phase: "confirming"` immediately before serial confirmation
(`run.go:3866-3881`). A confirmation spanning a tick receives a correct immediate
notification followed by a heartbeat still saying executing mutants.

Decisions also unconditionally set executing mutants, including cached and
skipped decisions (`server.go:591-595`). Preparation callbacks can overwrite the
label during pipelined execution. A last-selected event label is not necessarily
the current active phase.

## Required Regression

Exercise a token-bearing request across slow selection/signposting, execution,
confirmation, and final persistence/rendering. Assert cadence coverage and truthful
labels through every interval, including cached/skipped decisions and overlapping
preparation/execution. Inject the clock/cadence seam instead of requiring long
wall-clock sleeps. Keep tokenless policy, notification failure, and transport
cancellation separate; do not label a heartbeat as evidence of candidate progress.

Lands: cross-tool train chunk 207
