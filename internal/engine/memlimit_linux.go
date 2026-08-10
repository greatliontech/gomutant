package engine

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const memoryCeilingSupported = true

// totalRAMBytes reads MemTotal from /proc/meminfo; 0 when unreadable —
// an unknown total disables the derived default rather than guessing.
func totalRAMBytes() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}

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
