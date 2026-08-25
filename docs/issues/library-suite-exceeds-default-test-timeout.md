# The library package outgrew go test's default 10m timeout

On go1.27 the root-package suite runs ~16 minutes on the dedicated
dev machine, so a plain `go test ./...` panics at the 10m default
with an arbitrary victim test named — a verdictless run that reads
as a hang. Every scripted invocation needs an explicit `-timeout`
(40m is the working budget); nothing in-repo carries that today.

Lands: with the CI workflows (the explicit timeout lands in the
matrix job), or the first contributor-facing make/task runner.
