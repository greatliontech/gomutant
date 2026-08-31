# Installed binary skew leaves v11 findings stores unreadable (fleet sweep 2026-08-31)

The weekly fleet sweep reports SKEW on gomutant's installed binary:
binary revision 00a4cdc330aa vs repo last-build-input 8993c06715c9.
The same sweep's findings-store inspection then failed on three
estates — the gomutant, gofresh, and stipulator repos' `.gomutant`
stores (16K each) — with "findings document version 11 not understood
(this binary reads 4-10)": a newer gomutant wrote those stores at
document v11 and the installed reader lags it. The two facts are one
unit of work: the standing SKEW response (`go install` in this repo)
brings the reader to the writer's version, and long-lived readers
(the gomutant MCP server) restart on the upgraded binary. If a
freshly-installed HEAD still reads only 4-10, the v11 writer was some
other build and that diagnosis reopens here.

Disjoint from pb docs/issues/findings-store-format-v3.md, which owns
the opposite mismatch (pb's store at document v3, below the reader's
floor — regenerate or delete, a pb-side call).

Lands: cross-tool train chunk 113 (the next gomutant change-set gate;
its triage dispositions this — the reinstall is the whole fix, and
any campaign work at that chunk needs the current binary anyway).
