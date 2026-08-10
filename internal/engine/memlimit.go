package engine

import (
	"fmt"
	"os/exec"
	"sync/atomic"
)

// oracleMemoryLimit is the per-oracle-process memory ceiling in bytes,
// installed once per run before oracle work starts (SetOracleMemoryLimit);
// zero disables. Process-wide because the engine's oracle entry points are
// package functions and one process runs one campaign.
var oracleMemoryLimit atomic.Int64

// oracleMemoryInstalled distinguishes never-installed from explicitly
// disabled: a library caller that never chose gets the derived default
// at its first oracle (REQ-exec-oracle-memory's default clause), while
// an explicit negative stays off.
var oracleMemoryInstalled atomic.Bool

// memoryFloorBytes keeps the derived default above what a large test
// binary's in-oracle link step legitimately needs: a ceiling that breaks
// builds would convert every measurement into a discard.
const memoryFloorBytes = int64(1) << 30

// SetOracleMemoryLimit installs the per-oracle memory ceiling for this
// process's runs - between campaigns, never during one: oracle spawns
// read it live, so a mid-campaign change diverges evidence from the
// stamped pin. It configures: bytes > 0 is the explicit cap, 0 derives
// RAM / (2 x jobs) floored at 1 GiB (REQ-exec-oracle-memory), and a
// negative value disables the ceiling. On platforms without a hard-cap
// mechanism an explicit ceiling still applies as GOMEMLIMIT; the
// derived default additionally needs a readable RAM total, which only
// the Linux path currently provides.
func SetOracleMemoryLimit(bytes int64, jobs int) {
	oracleMemoryInstalled.Store(true)
	switch {
	case bytes < 0:
		oracleMemoryLimit.Store(0)
	case bytes > 0:
		oracleMemoryLimit.Store(bytes)
	default:
		oracleMemoryLimit.Store(DefaultOracleMemoryLimit(jobs))
	}
}

// OracleMemoryLimitBytes reports the installed per-oracle ceiling; 0
// means disabled. Consumers stamp it into measurement pins.
func OracleMemoryLimitBytes() int64 {
	return oracleMemoryLimit.Load()
}

// OracleMemorySnapshot captures the ceiling AND whether one was ever
// installed, so a caller can restore the exact prior state - a plain
// value round-trip cannot, because Set(0) derives rather than storing
// zero.
type OracleMemorySnapshot struct {
	limit     int64
	installed bool
}

// SnapshotOracleMemory captures the current ceiling state.
func SnapshotOracleMemory() OracleMemorySnapshot {
	return OracleMemorySnapshot{limit: oracleMemoryLimit.Load(), installed: oracleMemoryInstalled.Load()}
}

// RestoreOracleMemory reinstates a snapshot verbatim, the installed
// flag included.
func RestoreOracleMemory(s OracleMemorySnapshot) {
	oracleMemoryLimit.Store(s.limit)
	oracleMemoryInstalled.Store(s.installed)
}

// EnsureOracleMemoryDefault installs the derived default when no caller
// has chosen a ceiling yet - the library entry points' guard, so a
// consumer that never configured one still probes under the spec's
// default rather than uncapped.
func EnsureOracleMemoryDefault(jobs int) {
	if !oracleMemoryInstalled.Load() {
		SetOracleMemoryLimit(0, jobs)
	}
}

// DefaultOracleMemoryLimit derives the default ceiling: total RAM over
// twice the job count, floored at 1 GiB; 0 (disabled) when the total is
// unreadable — an unknown budget is never guessed.
func DefaultOracleMemoryLimit(jobs int) int64 {
	total := totalRAMBytes()
	if total <= 0 {
		return 0
	}
	if jobs < 1 {
		jobs = 1
	}
	limit := total / int64(2*jobs)
	if limit < memoryFloorBytes {
		limit = memoryFloorBytes
	}
	return limit
}

// oracleMemoryEnv appends the soft ceiling to an oracle environment:
// GOMEMLIMIT at ~90% of the hard cap, so a legitimately large oracle
// collects garbage against the ceiling instead of dying on it, while a
// runaway allocation still meets the hard cap
// (REQ-exec-oracle-memory). The go tool, the link step, and the test
// binary all inherit it.
func oracleMemoryEnv(env []string) []string {
	limit := oracleMemoryLimit.Load()
	if limit <= 0 {
		return env
	}
	soft := limit - limit/10
	return append(append([]string(nil), env...), fmt.Sprintf("GOMEMLIMIT=%d", soft))
}

// startOracleCeiling installs the hard cap on a just-started oracle
// process; descendants inherit the rlimit.
func startOracleCeiling(cmd *exec.Cmd) {
	applyMemoryCeiling(cmd, oracleMemoryLimit.Load())
}
