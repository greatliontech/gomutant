# Installed gomutant binary skews from repo HEAD (fleet sweep 2026-09-07)

The weekly fleet sweep's binary-provenance check found the installed
`gomutant` built from `e3eecfdf939f` — the commit that landed the one
view set serving the decision and producer roles (train chunk 92) —
while HEAD had moved to `810ed044a88c` (the consent-source pin backfill
across the record store). HEAD has since moved again, to
`ec02a4f89049`: the run's oracle bounds becoming a run-owned value
instead of process state (train chunk 96), a change to the campaign
and ephemeral paths the fleet's own adversarial loops run through. The
installed binary therefore lacks a landed behavioural change, not only
a records commit.

The engine's skew guards refuse loudly at use where provenance is
checked; the sweep catches the drift before a session trips on it. The
standing response is the cheap one: `go install` in this repo at HEAD,
restart any long-lived reader (the MCP server), and close this filing.
The check is deliberately coarse — any commit moves HEAD, docs included
— so the filing carries no regression claim, only the fact that the
installed tool and the tree disagree.

Lands: cross-tool train chunk 130 (gomutant's next chunk in the
register's execution order; its close reinstalls the binary at HEAD —
any earlier gomutant session's `go install` closes this the same way).
