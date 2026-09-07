# Standing vouch sets have no repo-level source on gomutant's side

pew reads a store's reviewed standing vouch set from the `vouches`
file at its store root (pew REQ-pew-vouch-source), so a pew consumer's
audited set has one home and the fleet sweep's mirrored flag list can
go. gomutant judged runs still take the same set only as `--vouch`
flags: tugboat's six-entry set lives in its prose and is hand-mirrored
into the sweep's gomutant invocation — the drift the pew side just
closed, still open here. The unsafe direction (an extra vouch
suppressing a real unverifiable) does not self-announce.

Two shapes, the second the collapse:

- gomutant reads `.gomutant/vouches` beside its findings document
  with pew's grammar and precedence (the flags extend, never remove),
  a per-tool twin of pew's file.
- gofresh owns the convention: a repo-level vouch file the engine
  reads for every consumer (a `WithDynamicStateVouchFile` default at
  the module or tree root), so pew, gomutant, and stipulator judge
  under one reviewed set without each re-implementing the reader — a
  gofresh spec change (the vouch set becomes a per-repository input
  the engine sources itself) and a release, then bumps.

Invariants preserved either way: the load-bearing set rides each
record as it does today; a flag never removes a reviewed acceptance.

Lands: cross-tool train chunk 162 (gofresh owns the convention; the gomutant twin file is not built)