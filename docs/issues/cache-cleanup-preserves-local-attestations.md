# Safe cache cleanup must preserve local attestations

Lands: tooling-redesign 7.9b, after gofresh's shared memo retention in 7.9a.

## Evidence and diagnosis

The user's 2026-10-15 disk-pressure report measured `~/.cache/gomutant`
at 4.0 GiB: 3,804.7 MiB in 2,890 local finding files and 228.2 MiB
in 27 `baselines.json` files. A recursive JSON inventory found nonempty
attestation-named fields in 70 finding files; this is an inventory signal,
not a judgment that those attestations remain applicable.

`store.go` places machine-local findings under the user cache directory.
Those findings can contain human equivalence reasoning that remeasurement
cannot recover; the results contract also protects legacy records carrying
that reasoning. `bank.go` stores disposable baseline evidence beside them.
The baseline reader rejects files above 8 MiB, but that read ceiling does
not reclaim their disk space. There is no general cache-cleanup command;
`prune` removes unresolved symbols rather than managing disk retention.

## Required outcome

Expose usage, preview, and safe reclamation of recomputable data, including
the shared gofresh memo store. Preserve human-authored dispositions and
legacy records by default; never silently equate a user-cache path with
disposable evidence. Make retained records and their size visible. Any
explicit record-discard operation must clearly identify its data loss.

Bound disposable baseline retention without treating age as evidence
invalidity. Coordinate cleanup with active campaigns and atomic publication;
never unlink persistent lock inodes. Preserve repository findings,
exemptions, and ephemeral attestations. If storage separation moves existing
local records, preserve their content and lookup behavior through migration.

Verify cleanup against attested, unattested, legacy, malformed, oversized,
and concurrently accessed stores. Missing disposable evidence must cause
remeasurement, never authorize a result. Document cold-run costs and the
record/cache distinction in operator guidance.
