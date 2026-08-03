# Store.Update re-parses the whole overlay per finding commit; bloat-era residue is never quarantined

**Lands:** overlay reads are per-symbol (or one cached parse per run)
AND a healthy-format guard quarantines oversized/stale-provenance
overlay entries — together restoring O(entry) commit cost on a repo
whose overlay carries pre-fix residue.

Two coupled defects, observed on the tugboat repo with the post-
union-view binary (6f00495, built 2026-08-03):

- **O(whole overlay) parse on every Update.** `Store.Update`
  (store.go:208) runs `loadOverlay` (store.go:92) inside
  `UpdateDocumentContext`, and `loadOverlay` reads AND
  `ParseFindings`-parses **every** overlay entry — on **every**
  finding commit (`commitFinding`, run.go:1458). The overlay layout
  is per-symbol files precisely so per-symbol access is possible;
  the merge needs only the symbols the update touches (or one parse
  cached across the run's commits — the run already holds the
  document lock). As shipped, per-commit cost is O(total overlay
  bytes), so a campaign's own overlay growth makes the tail
  quadratic even with healthy small entries, and any large residue
  makes every commit cost minutes.
- **Bloat-era residue passes the hygiene check.** `loadOverlay`
  skips-and-removes malformed entries ("the overlay is a cache,
  never a record of note") but a well-formed entry of pathological
  size from the pre-fix evidence format parses fine and is carried
  forever. The tugboat overlay
  (`~/.cache/gomutant/repos/9181eaa3a65012fb8523a9fc/findings/`)
  holds 56 entries, 7.8 GB total, sized 447–886 MB each — the
  2026-08-01 aborted campaign's `runtimeInputs` bloat. A cache with
  a freshness discipline for content needs one for cost: an entry
  over a sane evidence ceiling (or with pre-fix provenance) is
  exactly as evictable as a malformed one.

Measured: `gomutant run --symbol
'github.com/greatliontech/tugboat/internal/raft.RawNode.LastIndex'
--budget 0` (a one-line getter, 2 candidates) ran 17+ minutes
single-core-pegged at 31 GB RSS with zero verdicts before being
SIGQUIT'd; utime:stime 102529:2698 (~97% userspace — pure parse, no
IO churn; the old snapshot-churn pathology did NOT reproduce). The
dump's runnable goroutine:

    encoding/json.(*Decoder).Decode          (buffer len 0x1aab3ccc ≈ 447 MB)
    gomutant.decodeKnownObject               findings.go:753
    gomutant.ParseFindings                   findings.go:361
    gomutant.(*Store).loadOverlay            store.go:112
    gomutant.(*Store).Update.func1           store.go:209
    gomutant.UpdateDocumentContext           findings.go:989
    gomutant.(*Store).Update                 store.go:208
    gomutant.commitFinding                   run.go:1458
    gomutant.(*Tree).Run                     run.go:1394

RSS ≈ decoded object graphs of the multi-hundred-MB entries live
across repeated per-commit parses. Reproduce: seed any repo's
overlay dir with one ≥400 MB valid v3 entry, run any one-symbol
`run`, watch every commit re-pay the parse.

Interim relief applied on the consumer side (tugboat): the residue
entries were deleted (cache semantics; their salvage value was
already void — the wal oracle set changed since the abort, so
freshness would re-measure regardless). That restores usable runs
there but leaves both defects live: the next long campaign's own
overlay re-grows the per-commit parse cost, and any future format
regression would again poison every subsequent run.
