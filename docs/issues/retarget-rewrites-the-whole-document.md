# A prefix retarget rewrites most of the findings document

Lands: cross-tool train chunk 282 (record verbs over both layers — stable table emission; the rename's re-digest cost measured at its open)

A `retarget` over a package rename that touched 21 of 290 records
re-emitted nearly the whole findings document: the runtime-inputs
table came back in a different order, and 144 records' evidence rows
changed content. The rename was a pure path move (a test-support
package relocated under a new parent directory, its files
byte-identical), so no closure's content changed; only the symbol
identities under the old prefix did.

Field report (greatliontech/pb, findings document version 12, 290
records, the gomutant build installed before 9881159 — its exact
revision unrecorded, at or before a570775):

```
gomutant retarget \
  --from 'github.com/greatliontech/pb/internal/gittest.' \
  --to   'github.com/greatliontech/pb/internal/testing/gittest.'
retargeted 21 record(s)
```

Comparing the document before and after (pb commit 250f2cf, the
parent against the commit):

- `runtimeInputsTable`: 28 entries before and after, the same set,
  one entry moved from index 11 to index 24 and every entry between
  shifted down by one; the table is sorted neither before nor after.
- `evidenceTable`: 1164 entries before and after; 477 identical in
  position; the differing entries carry different `maximalClosure`
  and `observationEvidence` digests for otherwise matching rows.
- `ledgerTable`: 47 entries, the same set, 20 in the same position.
- `findings`: 290 records, 146 identical; 21 name the moved package
  before and after.
- The diff is 30604 lines each way over a document of about that
  size; whitespace-insensitive it is 30564.

Two symptoms to separate at triage. The first is serialization: an
unordered table re-emitted in visit order rewrites every reference
into it, so a document with no semantic change diffs whole; a stable
emission order (the existing order kept, or a canonical one) would
make a retarget's diff the retarget's rows. The second is semantic
and the one that matters for the record: 144 records' evidence
digests changed under a rename of 21, which reads as every closure
containing a renamed symbol re-digested. REQ-result-lifecycle says a
retarget rewrites symbol identity while "attestations and survivors
ride unchanged"; whether the re-digested evidence still proves fresh
at the next campaign, or the rename silently costs those 144 records
a re-measurement, is the question the triage should answer with a
campaign over the retargeted document. The document was verified
consistent after the move (no identity under the old prefix
remains; the 21 records resolve), so nothing is known to be lost.
