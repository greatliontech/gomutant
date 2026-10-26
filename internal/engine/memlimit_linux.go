package engine

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

const memoryCeilingSupported = true

// applyMemoryCeiling installs the hard cap on a started oracle process:
// RLIMIT_DATA (data-segment mappings, which is where Go allocations
// land on modern kernels — RLIMIT_AS would break the runtime's large
// virtual arena reservations) applied via prlimit so every descendant
// inherits it. Best-effort: a process that exited before the limit
// landed is already contained by its own death, and an EPERM leaves
// the GOMEMLIMIT soft ceiling as the whole defense.
func applyMemoryCeiling(cmd *exec.Cmd, limit int64) {
	if limit <= 0 || cmd.Process == nil {
		return
	}
	value := uint64(limit)
	rlimit := unix.Rlimit{Cur: value, Max: value}
	_ = unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_DATA, &rlimit, nil)
}
