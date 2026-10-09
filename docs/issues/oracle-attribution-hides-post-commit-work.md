# Oracle attribution hides post-commit work

Lands: cross-tool train chunk 267, with campaign progress and close-out reporting.

A derived-oracle campaign can finish mutation measurement and commit one target,
then spend most of its remaining lifetime in synchronous instability attribution
without exposing that phase or its test progress. The audit event also replaces
the latest execution event with one lacking candidate counters, displaying `0/0`
after an already populated candidate count. Heartbeats continue, but cannot tell
the operator what is running or how much attribution work remains.

Weaver reproduced this with gomutant v0.64.0 (`659da7794e52ead52561343a9845cb3425164bdb`),
five targets, two candidates per target, four jobs, a ten-second analysis budget
and a two-hour command timeout. Run `9a2a4bb3eb289654` committed its first target
at about 27 minutes; the same one-of-five count remained through cancellation at
76 minutes. The banked target retained two kills. Four targets remained incomplete.
The command did not exceed its deadline; this is not evidence of a deadline fault
or deadlock.

At that revision, `commitAndAttribute` calls `emitOracleGuidance` synchronously
after committing. `probeOracleInstability` then visits every test in the exact
derived oracle set, serially. `TestProbeObservedEnv` normally executes a passing
nonempty observation twice. The sweep uses the campaign context and advisory
leash, not the precise-analysis budget; derived mode supplies a one-hour baseline
leash to each probe. A sampled child command was:

```text
go test -json -timeout 1h0m1s -count=1 -run ^TestOwnerResolutionUsesHostingCustodyForEveryResourceAddress$ github.com/thegrumpylion/weaver/internal/daemon -test.testlogfile=/tmp/gomutant-probe-4036385513/baseline.testlog
```

The attribution loop emits no per-test progress. Separately, the command reporter
replaces its execution snapshot on the audit event, losing candidate counts that
the event does not carry. The two mechanisms jointly explain the misleading
surface. The observation causing attribution and exact durations of individual
probes cannot be recovered from these heartbeat lines alone.

Preserve candidate counters across non-candidate events and expose attribution
phase, current test, completed count and repeat activity. Give advisory work an
explicit execution bound, or schedule it so it cannot prevent other measured
targets from being committed. Any bound must leave attribution incomplete rather
than imply stable runtime evidence. Preserve the full derived oracle and measured
findings; excluding integration tests is not a reporting fix.

An explicit oracle containing exactly the same tests avoids the advisory sweep
without changing the test selection. This is a caller workaround, not a fix for
the derived-oracle progress contract.
