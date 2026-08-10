//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processExecutionSupported = true

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd
}

// runOracleProcess starts the oracle, installs the hard memory ceiling
// on the live process (descendants inherit the rlimit), and waits -
// cmd.Run with the prlimit window in between. The window before the
// limit lands is milliseconds against a runaway that needs seconds to
// matter (REQ-exec-oracle-memory).
func runOracleProcess(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	startOracleCeiling(cmd)
	return cmd.Wait()
}
