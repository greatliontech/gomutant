# The persistent campaign lock lands in consumers' commits

Lands: cross-tool train chunk 38a (tool-minted ignore beside the
tracked document so add-everything consumer loops cannot commit the
lock, plus the lifecycle line in consumer docs).

## Observed (2026-08-14, ocifs)

`AcquireCampaignLock` deliberately never removes
`<document>.campaign` (the flock-unlink race documented at
campaignlock_unix.go:52-57 — the flock is the lock, stale content is
harmless). But consumers track `findings.json` in git, and loops
that stage with `git add -A` sweep the sibling in: ocifs committed a
lock file containing `pid 3862207 since 2026-08-14T16:32:34+03:00`
before noticing, and had deleted "stray" `.campaign` files in two
earlier sessions on the belief they were leaked temp documents. The
persistence-by-design is invisible to a consumer who has only the
file name; nothing in the refusal path or docs says the file is
supposed to outlive the campaign and should be gitignored.
