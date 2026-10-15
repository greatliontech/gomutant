# Installed binary built from a dirty tree (fleet sweep 2026-10-05)

The 2026-10-05 weekly fleet sweep's binary-provenance check reported the installed
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

The installation obligation covers both binary files and long-lived readers:
replacing a file does not replace an already-running MCP process.

Re-observed by the 2026-10-12 sweep with the fact moved: the line now
reads SKEW — binary `vcs.revision=2172a8b42bb0`, last build-input
commit `4b15304438c3` — and the modified flag is gone. The clean
install at chunk 311's close did happen (the binary anchors to a
commit again), and the tree has since taken further build-input
commits, so the installed binary trails HEAD. The remedy is the same
`go install` from the committed tree; this doc keeps tracking the
provenance line rather than minting a second trigger for the skew.

The current source audit supplied for this filing records both installed files
(`~/go/bin/gomutant` and `~/.local/bin/gomutant`) at clean revision `1e5ff06`,
with `modified=false`. The old dirty/skewed file state is no longer the current
claim. The live MCP process identity remains unknown and the next fleet sweep
has not been completed. Close against actual file and process provenance, not
an inferred restart; this document edit performs neither installation nor sweep.

Lands: tooling-redesign 9.3.
