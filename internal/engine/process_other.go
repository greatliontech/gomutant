//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package engine

import (
	"context"
	"os/exec"
)

const processExecutionSupported = false

// oracleCommand prepares the command under the tree's runner; on a
// host without process-group ownership the runner's containment is
// the wait delay alone, and execution is refused earlier during tree
// loading, so this exists for compilation completeness only.
func oracleCommand(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	return goRunner.Command(ctx, dir, env, args...)
}

// runOracleProcess on hosts without process-group ownership just runs
// the command; execution is refused earlier during tree loading, so
// this exists for compilation completeness only.
func runOracleProcess(cmd *exec.Cmd, bounds OracleBounds) error {
	return cmd.Run()
}

// oracleProcessKilled mirrors the unix reading for compilation
// completeness (execution is refused earlier during tree loading).
func oracleProcessKilled(cmd *exec.Cmd) bool {
	return cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == -1
}
