package engine

import "runtime"

// OracleBounds are the resource bounds one run — a campaign or an
// ephemeral probe — applies to every oracle process tree it spawns:
// the per-process memory ceiling (REQ-exec-oracle-memory) and the
// inner-parallelism width (REQ-exec-oracle-parallelism). A zero axis
// is unbounded. The bounds are a value the run derives once and hands
// to each spawn, never process state: two runs in one process — a
// campaign and a probe, two probes — each spawn under their own, so a
// run's baseline and its mutants always share one ceiling and one
// width (differential attribution's "differ in the overlay alone").
type OracleBounds struct {
	MemoryBytes int64
	Width       int
}

// DeriveOracleBounds derives a run's bounds from its memory choice and
// job count: memoryBytes > 0 is the explicit ceiling, 0 derives total
// RAM over twice the job count floored at 1 GiB (0 when the total is
// unreadable — never guessed), negative disables; the width is
// max(1, NumCPU/jobs). On platforms without a hard-cap mechanism the
// ceiling still applies as GOMEMLIMIT; the derived default needs a
// readable RAM total, which only the Linux path currently provides.
func DeriveOracleBounds(memoryBytes int64, jobs int) OracleBounds {
	b := OracleBounds{Width: oracleParallelismWidth(jobs)}
	switch {
	case memoryBytes < 0:
		b.MemoryBytes = 0
	case memoryBytes > 0:
		b.MemoryBytes = memoryBytes
	default:
		b.MemoryBytes = DefaultOracleMemoryLimit(jobs)
	}
	return b
}

// oracleParallelismWidth derives the per-tree width: host width over
// the job count, floored at one.
func oracleParallelismWidth(jobs int) int {
	if jobs < 1 {
		jobs = 1
	}
	return max(1, runtime.NumCPU()/jobs)
}
