# Concurrent campaigns on one server stomp the process-wide width

Lands: cross-tool train chunk 41.

The campaign lock (REQ-exec-exclusivity) excludes concurrent campaigns
per findings document, but a long-lived MCP server accepts concurrent
runs against different documents, and each installs the process-wide
inner-parallelism width from its own job count. Within one campaign
the evidence is safe - the run derives its one evidence environment at
start and every spawn, ingest, merge, and splice judges under that
value, and the engine-level compositions never widen an already-capped
environment - but a second campaign installing a NARROWER width can
make the first campaign's later spawn-time compositions rewrite the
delivered GOMAXPROCS downward, splitting those oracles' recorded
environment from the campaign's evidence environment: their evidence
degrades or re-measures, never serves stale (the never-widen rule
keeps the split fail-safe).

Fix direction, for the chunk-41 MCP surface audit: either serialize
width installation per server (a second campaign with a different
job count waits or refuses, symmetric with the ephemeral memory
ceiling's in-flight refusal), or thread the campaign's evidence
environment into the engine spawn sites so the process-wide atomic
stops being a spawn-time input entirely.
