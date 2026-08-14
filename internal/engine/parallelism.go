package engine

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// oracleParallelWidth is the per-oracle-process-tree inner-parallelism
// width in scheduler threads, installed once per run before oracle work
// starts (SetOracleParallelism); zero leaves oracle trees uncapped.
// Process-wide for the same reason the memory ceiling is: the engine's
// oracle entry points are package functions and one process runs one
// campaign.
var oracleParallelWidth atomic.Int64

// SetOracleParallelism installs the inner-parallelism cap for this
// process's runs - between campaigns, never during one, like the memory
// ceiling: each oracle tree's width becomes max(1, NumCPU/jobs), so the
// campaign's jobs concurrent trees together stay within the host
// instead of each spawning a full-width toolchain tree - jobs x NumCPU
// runnable threads, quadratic in cores at the default job count
// (REQ-exec-oracle-parallelism). The width is a scheduling bound, never
// a measurement pin: it reaches verdicts only through the wall-clock
// oracle timeout, exactly as ambient host load does.
func SetOracleParallelism(jobs int) {
	oracleParallelWidth.Store(int64(oracleParallelismWidth(jobs)))
}

// OracleParallelismWidth reports the installed per-tree width; 0 means
// uncapped. A read-only surface for callers auditing the installed
// bound - the width is never a measurement pin.
func OracleParallelismWidth() int {
	return int(oracleParallelWidth.Load())
}

// OracleParallelismSnapshot captures the installed width so a scoped
// override (a probe between campaigns) can restore the exact prior
// state.
type OracleParallelismSnapshot struct {
	width int64
}

// SnapshotOracleParallelism captures the current width state.
func SnapshotOracleParallelism() OracleParallelismSnapshot {
	return OracleParallelismSnapshot{width: oracleParallelWidth.Load()}
}

// RestoreOracleParallelism reinstates a snapshot verbatim.
func RestoreOracleParallelism(s OracleParallelismSnapshot) {
	oracleParallelWidth.Store(s.width)
}

// oracleParallelismWidth derives the per-tree width: host width over
// the job count, floored at one.
func oracleParallelismWidth(jobs int) int {
	if jobs < 1 {
		jobs = 1
	}
	return max(1, runtime.NumCPU()/jobs)
}

// oracleEnv composes the per-oracle resource bounds onto a spawn
// environment: the soft memory ceiling and the inner-parallelism cap.
// Every oracle spawn site routes its environment through this one
// composer, so a mutant run and the baseline probe that attributes its
// failure always execute under identical bounds - differential
// attribution is sound only when the two runs differ in the overlay
// alone.
func oracleEnv(env []string) []string {
	return oracleCPUEnv(oracleMemoryEnv(env))
}

// oracleCPUEnv appends the inner-parallelism cap to an oracle
// environment as GOMAXPROCS, which the go tool, its compile and link
// workers, and the test binary (its t.Parallel width included) all
// honor - the go tool's package-build -p defaults to its own
// GOMAXPROCS, so this one entry bounds the build dimension too, and an
// explicit flag is deliberately not emitted: it would override an
// operator's narrower ambient GOMAXPROCS or GOFLAGS bound, which the
// env default never does. The cap only ever narrows: an environment
// already carrying a narrower GOMAXPROCS keeps it - overriding would
// raise the operator's own bound (REQ-exec-oracle-parallelism).
func oracleCPUEnv(env []string) []string {
	width := oracleParallelWidth.Load()
	if width <= 0 {
		return env
	}
	if ambient, ok := envGOMAXPROCS(env); ok && int64(ambient) <= width {
		return env
	}
	return append(append([]string(nil), env...), fmt.Sprintf("GOMAXPROCS=%d", width))
}

// envGOMAXPROCS reports the environment's effective GOMAXPROCS - the
// last entry, when well-formed and positive, matching os/exec's
// duplicate-key semantics. A malformed last entry reports absent, so
// the cap's append (which wins the duplicate) narrows it.
func envGOMAXPROCS(env []string) (int, bool) {
	return envGOMAXPROCSFold(env, runtime.GOOS == "windows")
}

// envGOMAXPROCSFold is envGOMAXPROCS with the key-case rule explicit:
// Windows environment lookups are case-insensitive, so a lowercase
// gomaxprocs entry is the same variable there and must count as the
// effective ambient value - missing it would append a wider entry that
// os/exec's case-insensitive dedup lets win, widening the operator's
// bound. Unix keys are case-sensitive and fold nothing.
func envGOMAXPROCSFold(env []string, foldCase bool) (int, bool) {
	value, found := 0, false
	for _, entry := range env {
		key, rest, ok := strings.Cut(entry, "=")
		if !ok || key != "GOMAXPROCS" && !(foldCase && strings.EqualFold(key, "GOMAXPROCS")) {
			continue
		}
		n, err := strconv.Atoi(rest)
		found = err == nil && n > 0
		value = n
	}
	return value, found
}
