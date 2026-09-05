# Reading unsupported older overlays deletes attestation reasoning

Lands: cross-tool train chunk 139

`ParseFindings` refuses document versions below
`OldestReadableDocumentVersion` (currently 4). In `Store.loadOverlay`,
that refusal is treated as malformed cache data and reaches `os.Remove`.
The whole load can then succeed with the older finding missing. Only
`ErrVersionAhead` preserves the file and refuses the read.

An unsupported old record is not necessarily corrupt. Local overlays can
contain authored equivalence attestations as well as reproducible
measurement evidence. Deleting their `attested` records loses the
position, operator, and reason; remeasurement cannot recover the judgment.
This is the older-version counterpart of the version-ahead preservation
repair in commit `145a8fb`, which is already present in v0.52.0.

The VMM cache contains 38 v2 overlays, 12 containing attestation arrays.
One is a finding for `resource.ClaimedResources.Validate` whose
attestation explains why changing unspecified diagnostic text preserves
validation and lifecycle behavior. The VMM's empty v3 repository findings
document currently refuses before overlay loading; removing or replacing
that document would expose the surviving cache records to deletion.
The source path demonstrates the risk; no destructive probe has been
run against that cache.

Reproduce in disposable storage, never the consumer's original cache:

1. Isolate `XDG_CACHE_HOME` and create a temporary module root.
2. Open its store with an absent repository findings path.
3. Put a valid v2, single-finding export containing an attestation into
   that store's computed overlay directory.
4. Call `Load` and compare the original file's bytes afterward.

At revision `4dcf96f`, `store.go` treats the unsupported-version parse
error as corruption and deletes the file. Expected behavior is a loud
unsupported-version refusal that leaves the bytes intact and serves no
unverified evidence. Test both older and newer unsupported versions,
with and without attestations, while retaining separately justified
malformed-record and size-limit handling.

Preserve legacy material before any recovery operation opens the store.
Do not relabel a nonempty document with a newer version number or treat
old evidence as current without the required migration or remeasurement
and renewed equivalence judgments.
