# The campaign's baseline refusal discards the oracle's output

The ephemeral verb's failing-baseline refusal names the failing tests
and carries their own output. The campaign's baseline probe
(TestProbeObservedEnv) computes the same diagnostic and discards it:
a target skipped on a failing baseline names the failing tests on its
decision line and nothing of what the oracle saw, so a baseline that
fails under the campaign and passes for the caller's plain go test is
undiagnosable from the run. The decision line is the wrong home for a
multi-line diagnostic; the reporter's note channel is.

Lands: cross-tool train chunk 156.3 (the reporter for every verb: the
baseline failure's diagnostic rides a note beside the skip decision).
