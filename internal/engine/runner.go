package engine

import (
	"os/exec"

	"github.com/greatliontech/gofresh/gotool"
)

// goRunner is the one runner every go command gomutant owns rides
// (REQ-exec-go-command-runner): the oracle spawns — the mutant run,
// its baseline probe, the coverage probe — the tree's own listings and
// environment snapshots, the toolchain ladder's sampler, the roots
// probe of every observation ingest, and, installed on the analysis
// engines (gofresh.WithGoRunner), every go command they spawn
// themselves. Its containment is the policy's process boundary — the
// child in its own process group, a cancellation sweeping the group,
// an already-gone group the process-done case, the reap bounded by
// the policy's wait delay — with no quit grace: an oracle is killed
// outright, so a bound's expiry reads as the kill it is
// (oracleProcessKilled) and never as a test binary's own
// goroutine-dump exit. The oracle's resource policy — the memory
// ceiling, the group's niceness — is applied after the spawn over the
// prepared command (runOracleProcess), where gofresh has no seam; the
// Windows oracle alone carries its own containment, the job object
// (process_windows.go), under the same hook. A command that exited
// cleanly while a descendant held its pipe past the policy's wait
// delay answers with what it wrote: the listing serves it
// (LinkedTestPackagesContext), the toolchain sample serves its first
// line (gofresh's rule).
var goRunner = gotool.Runner{
	Containment: &gotool.Containment{},
	Prepare:     observeGoCommand,
}

// observeGoCommand is the runner's hook on every prepared go command,
// the Windows oracle arm's included: it hands the command to the test
// observer when one is installed (seams.go) and is otherwise inert. An
// observer reads the command's directory and arguments, never its
// boundary: on Unix the hook runs after the containment is installed,
// on the Windows oracle arm before the job shape is (the job must be
// installed over the prepared command).
func observeGoCommand(cmd *exec.Cmd) {
	if observe := goCommandObserver; observe != nil {
		observe(cmd)
	}
}

// GoRunner is the tree's go-command runner, for the analysis engines
// the root builds and the served face's environment snapshot.
func GoRunner() gotool.Runner { return goRunner }
