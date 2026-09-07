package engine

import (
	"fmt"
)

// memoryFloorBytes keeps the derived default above what a large test
// binary's in-oracle link step legitimately needs: a ceiling that breaks
// builds would convert every measurement into a discard.
const memoryFloorBytes = int64(1) << 30

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
func oracleMemoryEnv(env []string, limit int64) []string {
	if limit <= 0 {
		return env
	}
	soft := limit - limit/10
	return append(append([]string(nil), env...), fmt.Sprintf("GOMEMLIMIT=%d", soft))
}
