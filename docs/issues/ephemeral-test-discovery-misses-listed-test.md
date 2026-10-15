# Ephemeral test discovery misses a test listed by Go

Lands: tooling-redesign 5.4.

## Observation

In Weaver, a test-scoped ephemeral probe reported that its pattern matched no
tests and there was nothing to attribute, after loading. Go's test listing with
the same package and anchored pattern listed the named test successfully.
The probe is **unmeasured**: this report establishes neither a killed mutation
nor a surviving mutation, and does not establish three completed mutant runs.

The existing `TestSubscriptionTimeoutSpansSDKRetries` function had its whole
body wrapped in standard-library `testing/synctest.Test`. This is reproduction
context, not evidence that synctest causes the discovery discrepancy. The root
cause is unknown; an installed-tool change, loader behavior, or cache state are
unconfirmed possibilities, not diagnoses.

## Environment and reproduction

The commands below were reported by the caller from
`/home/nikolas/repos/github.com/thegrumpylion/weaver`. At filing, lightweight
version inspection in that directory returned:

```text
go version go1.27.2-X:nodwarf5 linux/amd64
gomutant v0.64.0 (findings document version 14, reads 4-14)
```

Weaver HEAD at filing was `ad0b619fb60a84c5104fde734487f79bde4f5e6d`.
The reported reproduction includes working-tree edits; that commit alone is
not claimed to reproduce the test-body wrapper. The installed versions above
were inspected at filing, not captured as part of the original failed probe.

The inspected payload at `/tmp/opencode/weaver-timeout-outer.json` was:

```json
{"edits":[{"file":"internal/provider/anthropic/subscription.go","old_string":"requestCtx, cancel := context.WithTimeout(ctx, timeout)","new_string":"requestCtx, cancel := context.WithTimeout(ctx, timeout*2)"}]}
```

The source path in this payload is relative to Weaver, not gomutant. From the
Weaver working directory, the reported probe command was:

```sh
mlock run gomutant ephemeral --batch /tmp/opencode/weaver-timeout-outer.json --test-pkg ./internal/provider/anthropic --run '^TestSubscriptionTimeoutSpansSDKRetries$' --runs 3
```

Reported result: after loading, the pattern matched no tests and nothing could
be attributed. This is a summary of the diagnostic, not a captured verbatim
transcript.

The comparison command was:

```sh
mlock run go test -short -list '^TestSubscriptionTimeoutSpansSDKRetries$' ./internal/provider/anthropic
```

It printed `TestSubscriptionTimeoutSpansSDKRetries` and passed in `0.011s`.
Listing establishes Go's discovery of the test, not execution of its assertions
or mutation sensitivity. These probe and listing commands were not rerun while
filing this issue.

## Expected behavior and resolution

Current full-oracle@3 admission includes Go's alias-shaped runnable declarations.
That repair does not diagnose or close either Weaver observation below: the
reported tool identity is older, its original failed call did not capture that
identity, and neither mutation was measured. Preserve both cases until the
actual effective selection and source state are established.

A second reproduction is not a synctest-shaped test. Weaver's causal-membership
working tree produced the same mismatch for ordinary Go tests:

```sh
gomutant ephemeral --batch /tmp/opencode/weaver-causal-token.json --test-pkg ./internal/invocation/gmdbstore --run '^TestCausalRechecksActualCommittingSnapshot$' --runs 3
```

The edit removes only `current != token` from
`g != expected || current != token || g == math.MaxUint64` in
`internal/invocation/gmdbstore/causal.go`. The probe again reported no matching test
and measured nothing. A campaign selecting `checkCausalParticipants` with oracle
`TestCausalMalformedHistoryAndBounds` skipped the target because the oracle was
"not a runnable test" (run `fa4ed2ff84ba766b`; zero measured targets).

In the same tree, Go listed both tests with
`go test -short -list '^TestCausal(RechecksActualCommittingSnapshot|MalformedHistoryAndBounds)$' ./internal/invocation/gmdbstore`,
then executed both successfully with `-short -race -count=1 -run` and the same
pattern (6.726s). Thus the observed discrepancy extends beyond the original
synctest wrapper. The cause remains unestablished; successful ordinary execution
does not measure either mutation.

A test-scoped ephemeral probe should discover the matching test that Go lists
under the same effective package and build selection, then measure and attribute
the requested edit. If the selections differ, that difference must be identified
before attributing the discrepancy to discovery or to the test's body.

Resolve by reproducing the discrepancy with captured effective toolchain,
installed-tool identity, build selection, and source state; determine its cause
and add a regression witness at the responsible boundary. Include the observed
standard-library synctest test shape without presuming it is causal. Confirm
that the matching scoped probe reaches measurement and reports its actual
mutation outcome. Investigation and implementation are deferred here at the
caller's explicit request to file the observation only.
