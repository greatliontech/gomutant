//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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

// oracleProcessKilled is the platform-owned fact "the oracle process
// did not exit on its own": on unix a bound expiry SIGKILLs the
// process group, recorded as ExitCode -1 (killed by signal, never
// exited) — a self-exited process reports its real code.
func oracleProcessKilled(cmd *exec.Cmd) bool {
	return cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == -1
}

// oracleNiceness is the absolute niceness every oracle process tree
// runs at: batch work that yields to interactive neighbors while a
// campaign saturates the host (REQ-exec-oracle-parallelism). Verdicts
// see it only through the wall-clock oracle timeout, like any ambient
// load.
const oracleNiceness = 10

// runOracleProcess starts the oracle, installs the hard memory ceiling
// on the live process (descendants inherit the rlimit), lowers the
// process group's scheduling priority (descendants inherit the
// niceness), and waits - cmd.Run with the two windows in between. Each
// window before a bound lands is milliseconds against work that needs
// seconds to matter (REQ-exec-oracle-memory,
// REQ-exec-oracle-parallelism). The priority drop is best-effort: a
// gomutant already running below oracleNiceness cannot lower its
// children to it, which is already the yielded state the drop exists
// to reach.
func runOracleProcess(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	startOracleCeiling(cmd)
	// Setpgid on the SysProcAttr above pins the child's pgid to its own
	// pid before exec, so the group id is its pid by the time Start
	// returns.
	_ = unix.Setpriority(unix.PRIO_PGRP, cmd.Process.Pid, oracleNiceness)
	return cmd.Wait()
}
