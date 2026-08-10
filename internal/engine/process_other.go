//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package engine

import (
	"context"
	"os/exec"
	"time"
)

const processExecutionSupported = false

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	return cmd
}

// runOracleProcess on hosts without process-group ownership just runs
// the command; execution is refused earlier during tree loading, so
// this exists for compilation completeness only.
func runOracleProcess(cmd *exec.Cmd) error {
	return cmd.Run()
}
