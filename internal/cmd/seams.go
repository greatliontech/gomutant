package cmd

import (
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

// commandSeams are the package's test injection points — the observer
// a test installs and the paces a test sizes — in one struct, read
// through the one variable seams. Production never writes a field:
// each stays at its default, so a seam is never a behaviour switch.
// The package's tests run serially; a test writes a field and
// restores it.
type commandSeams struct {
	// afterFinalReplacement observes the final replacement's return,
	// so a test can end the command exactly at the success boundary.
	afterFinalReplacement func()
	// stretchObserver sees every stretch a reporter names, in order, so
	// a test pins the sequence whatever the cadence; nil in production.
	stretchObserver func(label string)
	// progressInterval is the cadence of the phase-naming progress
	// line on the verbs whose cost is one call (a load, a judged
	// record, a prune, a retarget): the run and ephemeral verbs expose
	// the same cadence as a flag because their stretches are the
	// caller's to size. A seam so a test can lower it.
	progressInterval time.Duration
	// sigtermDrainDeadline bounds a SIGTERM-initiated drain: a supervisor
	// that sends SIGTERM follows with SIGKILL on its own clock, and
	// SIGKILL bypasses context cancellation entirely — in-flight oracle
	// process trees would survive as orphans and per-process scratch
	// would never sweep. The deadline is derived from the ecosystem's
	// smallest common kill window (docker stop defaults to 10s;
	// Kubernetes 30s; systemd 90s): 5 seconds drains what a short-oracle
	// campaign can bank and hard-cancels — processes reaped, scratch
	// swept — before ANY common supervisor escalates. An interactive
	// SIGINT keeps the unbounded patient drain: the human at the terminal
	// escalates by pressing Ctrl-C again. A seam for deadline-injection in
	// tests.
	sigtermDrainDeadline time.Duration
	// postCommitRenderBound bounds the rendering after the final
	// replacement (gomutant.PostCommitRenderBound in production): a seam
	// so the error exits the bound gates are reachable by a test.
	postCommitRenderBound time.Duration
}

// seams is the one variable tests write; defaultSeams is what
// production reads.
var seams = defaultSeams()

func defaultSeams() commandSeams {
	return commandSeams{
		progressInterval:      gomutant.ProgressCadence,
		sigtermDrainDeadline:  5 * time.Second,
		postCommitRenderBound: gomutant.PostCommitRenderBound,
	}
}
