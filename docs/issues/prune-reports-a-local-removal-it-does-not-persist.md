# A prune of a machine-local record reports the removal and keeps the record

Lands: awaiting triage

`prune` over a tree whose one unresolvable record lives in the
machine-local overlay reports it pruned, leaves it in the overlay,
and re-emits the repo document whole with no record changed; a
second `prune --check` names the same record again.

Field report (greatliontech/pb, findings document version 12, 284
repo records, the machine-local overlay under
~/.cache/gomutant/repos/<repo>/findings/, gomutant
v0.57.11-0.20260920133344-65af09f72c5f):

```
gomutant prune --check
would prune     github.com/greatliontech/pb/internal/plugin/oci.HostPlatform
would prune 1 record(s), 299 kept
gomutant prune
pruned     github.com/greatliontech/pb/internal/plugin/oci.HostPlatform
pruned 1 record(s), 299 kept
gomutant prune --check
would prune     github.com/greatliontech/pb/internal/plugin/oci.HostPlatform
would prune 1 record(s), 299 kept
```

After the prune the record is still in one overlay file
(`findings/1c79202f221ddd176a626d24.json` holds the symbol), and
`.gomutant/findings.json` differs from the committed document by
64810 lines each way with every record's content the same — the
whole-document re-emission the retarget issue also reports — so the
prune wrote the document it did not change and not the one it did.

The overlay is also the second document a rename must follow. The
consumer's textual retargets over the repo document (the tool's own
output for a package move, per the retarget issue) had left every
overlay record under the old package names until `prune --check`
named eleven of them as unresolvable; a textual rewrite of the
overlay files and `baselines.json` with the same prefixes brought the
preview down to the one dead record above. Whether the tool's
`retarget` rewrites the overlay is not stated by its guidance; if it
does, a rename done by hand on the repo document alone is a trap the
guidance could name, and if it does not, the overlay is a gap of the
retarget as well.
