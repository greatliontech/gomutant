# A package retarget drops one record, and the identical rerun refuses whole

Lands: awaiting triage

A `retarget` over a package rename reported every record rewritten
and wrote the document one record short; the same command over the
same input document, run again, refuses the whole batch naming that
record as a collision and writes nothing. The dropped record's symbol
resolves in the tree under the new prefix, with a body that had
moved since the record was measured.

Field report (greatliontech/pb at c99896a, findings document version
12, 284 records, gomutant v0.57.11-0.20260920055743-9881159d78c3):

```
gomutant retarget \
  --from github.com/greatliontech/pb/internal/trust. \
  --to   github.com/greatliontech/pb/internal/provenance/trust.
retargeted 25 record(s)
```

The document then holds 24 records under the new prefix and none
under the old: `internal/trust.parseExecution` — 132 candidates, 94
mutants, 91 killed, three attested survivors — is gone, its evidence
row with it, and nothing in the output says so.

The same command over the committed document again (the document
restored from the commit, at the tree root or through `--findings`):

```
gomutant: retarget: github.com/greatliontech/pb/internal/provenance/trust.parseExecution collides with an existing record
```

and the document is untouched. No record under the new prefix exists
in the document, the campaign overlay or the ephemeral attestations
before either run.

What distinguishes the record: its symbol resolves in the tree under
the new prefix, and the body there differs from the measured one
(`bodyHash` from an August commit; the function changed twice since).
`findings` over the committed document renders the record as stored,
its symbol resolving nowhere; over a document rewritten to the new
prefix it renders the same record re-judged against the live body —
116 candidates, the three attestations shed — so the retarget's
collision check may be meeting the re-judged view of the record it is
rewriting.

Two things to separate at triage: the first run's silent drop (a
retarget that reports N and writes N-1 is the flattering direction a
record verb must refuse), and the rerun's refusal. Recovered by hand:
a textual rewrite of the old prefix to the new across the committed
document is identical, every table reference expanded, to the tool's
output for each of the 283 records it kept, and holds the 284th;
that document is what the consumer committed.

A second gap the same rename shows, with no data lost: the
runtime-inputs table's entries are base64 documents naming absolute
paths under the old package directory (a rapid failure-file path
under `internal/trust/testdata/`), and the retarget — the tool's or
the textual one — rewrites symbol strings only, so every evidence row
over the moved package keeps a runtime-input reference to a path that
cannot exist. The judge reads the records stale for other reasons
today, so nothing observes it; a retarget that follows the package
directory into the runtime inputs, or a judgement that names the
moved path as its reason, would close it.
