# ephemeral refuses the "." test-package shorthand a module-local caller reaches for

`ephemeral --test-pkg .` from inside a workspace member module
refuses with `test package "." is not a loaded package import
path`; only the fully qualified import path is accepted, though
the `--dir` default is the same "." and `go test .` resolves it.
A module-local caller (a nested tools module in a workspace) has to
spell the full path every probe. Accepting a relative package
directory and resolving it against the loaded package set — the
same resolution `--package` globs already need — would match the
CLI's own directory default.

Lands: user decision
