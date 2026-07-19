# Staged snapshot mutation mode

Lands: when gomutant can run against the staged index or another explicit
content snapshot and produce clean evidence for that snapshot before a commit
exists.

## Observed

The consuming repository's change-review workflow stages the whole change set
before mutation testing. That staging is the checkpoint reviewers and mutation
probes are meant to read. A `gomutant run --changed HEAD` over the staged WIP
still records dirty provenance because the worktree is not committed, even when
the intended mutation subject is the exact staged content and no unstaged source
change participates.

This makes pre-commit mutation evidence harder to reuse. A clean content graph
exists in the index, but the findings record cannot distinguish "dirty because
the developer has not committed yet" from "dirty because an uncontrolled
worktree mutation may have changed what the compiler saw".

## Resolution

Add an execution mode that loads targets, oracle sources, build inputs, and
runtime observation baselines from a named content snapshot such as the Git
index. A staged run should pin evidence to that snapshot and refuse ordinary
unstaged source/build-input drift that would affect the measured package. The
resulting clean records can then enter the repo cache before the commit lands,
while genuinely local or runtime-unverifiable records still fall back to the
local cache.

The same mechanism can support other explicit snapshots, but the index form is
the hotpath because it matches the change-set boundary developers already use
for review and commit.
