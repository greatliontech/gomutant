# Eighteen MCP server witnesses carry no binding

internal/mcpserver/server_test.go holds eighteen tests no requirement
binds:

- TestAnalysisEventMessages
- TestCapResidueCountsTheRemainder
- TestCommandTimeoutDefaultsWhenOmitted
- TestCompactTargetDescriptionsDeduplicatesExactOracles
- TestDiscoverSchemaExplainsOracleReferences
- TestMCPBuilds
- TestToolAttestRefusesAMalformedExemptionsRecordBeforeWriting
- TestToolCandidateDiscardAccounting
- TestToolDiscover
- TestToolFindingsCapsSummaryRows
- TestToolRunCancellationAtUpdateLeavesDocumentUntouched
- TestToolRunCommandTimeoutLeavesFindingsUntouched
- TestToolRunCommandTimeoutPreservesOrdinaryErrors
- TestToolRunForwardsAnalysisEvents
- TestToolRunForwardsProgressNotifications
- TestToolRunPropagatesUpdateFailure
- TestToolRunWholeTreePrunesAlongsideCurrentMeasurement
- TestToolTimeoutInputsNameIndependentLimits

Each pins served behaviour a clause states (the run tool's
cancellation and timeout postures, the discover schema, the analysis
and progress forwarding, the residue caps), so a regression in any of
them leaves its clause green in a coverage judgment. Binding each to
its clause is a read of every test against docs/specs/mcp.md and
docs/specs/execution.md — the test-surface chunk's work, not a vouch
change set's.

Lands: cross-tool train chunk 219 (gomutant's test surface).
