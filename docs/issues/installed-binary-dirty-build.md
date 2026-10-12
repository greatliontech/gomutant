# Installed binary built from a dirty tree (fleet sweep 2026-10-05)

The weekly fleet sweep's binary-provenance check reports the installed
`gomutant` (`~/go/bin/gomutant`) stamped `vcs.revision=9afc5c673c5b`
with `vcs.modified=true`: DIRTY-BUILD. The revision is the last
build-input commit (chunk 306's landing, "an explicit oracle superset
re-measures survivors alone"), so this is not a SKEW — the binary
carries the latest landed inputs — but the stamp says the tree it was
compiled from carried uncommitted changes, and the toolchain records
only that there were some. The binary's content therefore corresponds
to no commit: whatever it carries beyond 9afc5c673c5b — an in-flight
change set, or nothing at all (an uncommitted docs file sets the same
stamp; the stamp cannot tell the two apart and the sweep reads the
stamp) — is unrecoverable from history, and the sweep's check cannot
vouch for a binary it cannot anchor.

The standing response is the SKEW response: `go install` in this repo
from a clean tree (committed HEAD, `git status` empty), then restart
long-lived readers (`gomutant mcp`). The sweep's next provenance line
reading `match` without the flag is the check that closes this doc.

Scanned: the index and all 49 docs it lists.
ephemeral-attestation-lifecycle and
evidence-fault-rendered-as-dirty-provenance concern the measured
TREE's dirtiness stamped into records, not the installed binary's
build stamp; the 2026-09-21 installed-binary-skew doc closed at chunk
282's install and is gone. No doc covers binary provenance, so the
rows are disjoint and no scheduled trigger is duplicated.

Re-observed by the 2026-10-12 sweep with the fact moved: the line now
reads SKEW — binary `vcs.revision=2172a8b42bb0`, last build-input
commit `4b15304438c3` — and the modified flag is gone. The clean
install at chunk 311's close did happen (the binary anchors to a
commit again), and the tree has since taken further build-input
commits, so the installed binary trails HEAD. The remedy is the same
`go install` from the committed tree; this doc keeps tracking the
provenance line rather than minting a second trigger for the skew.

Lands: the fleet sweep's next binary-provenance line reading `match`
without the modified flag — the `go install` from the committed tree
at gomutant's next chunk close in the active sequence
(tooling-redesign 5.4), or any earlier clean install, which closes it
sooner.
