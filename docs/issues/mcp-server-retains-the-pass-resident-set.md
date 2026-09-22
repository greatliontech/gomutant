# The MCP server keeps a finished pass's resident set while idle

The long-lived `gomutant mcp` server holds the resident set its last pass
reached after that pass has ended: nothing releases the loaded programs, the
memo, or the heap back to the host between requests, so an idle server costs
the host what its largest request cost, for as long as the session lives.

Field report (greatliontech/cerebro, gomutant 26565b7-era binary from the
project's `.mcp.json`, host 30 GiB with 4 GiB swap, 2026-09-22): the
session's MCP server, started 23.7 h earlier with the project's vouch flags
and with no request in flight, read VmHWM 2,697,416 kB and VmRSS 2,287,868 kB
(VmSwap 12 kB) — the second-largest process on the host after a live
stipulator resolve, ahead of every language server. A second Claude session's
server on the same host held 1.3 GB the same way. The sibling
`stipulator mcp` server on the same host sat at 15 MB RSS, so the retention is
this server's, not the transport's.

This is distinct from `observed-union-memory-slicing`, which bounds the
resident set *during* a pass: here the pass is over and the set stays. Two
shapes, the tool's to choose: release the pass's programs and memo at the end
of each request (the memo's persistent layer already serves shared folds
across passes, so an in-memory residue buys the next request little), and
return freed heap to the host when the server goes idle — the runtime's
scavenger returns it slowly on its own; an explicit return at request end
would make the idle server's footprint its true working set. A measured
before/after (VmRSS at idle after a run-class request) is the acceptance.

Lands: the cross-tool train's next triage gate.
