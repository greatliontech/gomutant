package engine

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// OracleEvidenceEnv is the environment oracle evidence digests under:
// the frozen tree environment with the inner-parallelism cap applied -
// exactly the ingest mirror's composition (PWD is per-package and
// recordless; the minted TMPDIR and the memory ceiling stay out by the
// mirror's stated contract). Serve-side revalidation, merge-time
// re-evaluation, and the analysis engines' declared producer env must
// all use this same environment: a stand-in without the injected width
// makes a width-reading oracle's evidence unreproducible - perpetual
// re-measure - or, when an ambient value matches an old record, serves
// stale across a width change (REQ-exec-oracle-parallelism).
func OracleEvidenceEnv(env []string, width int) []string {
	return oracleCPUEnv(env, width)
}

// oracleEnv composes the per-oracle resource bounds onto a spawn
// environment: the soft memory ceiling and the inner-parallelism cap.
// Every oracle spawn site routes its environment through this one
// composer, so a mutant run and the baseline probe that attributes its
// failure always execute under identical bounds - differential
// attribution is sound only when the two runs differ in the overlay
// alone.
func oracleEnv(env []string, bounds OracleBounds) []string {
	return oracleCPUEnv(oracleMemoryEnv(env, bounds.MemoryBytes), bounds.Width)
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
func oracleCPUEnv(env []string, width int) []string {
	if width <= 0 {
		return env
	}
	if ambient, ok := envGOMAXPROCS(env); ok && ambient <= width {
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
