//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package engine

import (
	"context"
	"os/exec"

	"golang.org/x/sys/unix"
)

const processExecutionSupported = true

// oracleCommand prepares an oracle's go command under the tree's
// runner (goRunner): the policy's directory and environment, and its
// containment — the child leads its own process group, a cancellation
// kills the group, the reap bounded by the wait delay.
func oracleCommand(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	return goRunner.Command(ctx, dir, env, args...)
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
func runOracleProcess(cmd *exec.Cmd, bounds OracleBounds) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	applyMemoryCeiling(cmd, bounds.MemoryBytes)
	// The runner's containment (Setpgid) pins the child's pgid to its
	// own pid before exec, so the group id is its pid by the time Start
	// returns.
	_ = unix.Setpriority(unix.PRIO_PGRP, cmd.Process.Pid, oracleNiceness)
	return cmd.Wait()
}
