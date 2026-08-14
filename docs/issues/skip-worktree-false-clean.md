# skip-worktree and assume-unchanged entries judge false-clean

`git status` omits index entries flagged skip-worktree or
assume-unchanged, so a worktree file diverging from the index (or
HEAD) under either flag stamps provenance clean while the measurement
read the divergent bytes. Symmetric across worktree and staged modes -
the flags are an operator opt-out of git's own change tracking - but
the staged mode's clean stamp now asserts equality with a named tree
(`stagedTree`), sharpening the misstatement. A fix would probe the
flags over selected paths (`git ls-files -v`) and refuse or stamp
dirty when set.

Lands: a field report shows a measured tree using either flag.
