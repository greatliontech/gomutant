package engine

import (
	"strconv"

	"github.com/greatliontech/gofresh/gotool"
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

// oracleCPUEnv sets the inner-parallelism cap on an oracle
// environment as GOMAXPROCS, which the go tool, its compile and link
// workers, and the test binary (its t.Parallel width included) all
// honor - the go tool's package-build -p defaults to its own
// GOMAXPROCS, so this one entry bounds the build dimension too, and an
// explicit flag is deliberately not emitted: it would override an
// operator's narrower ambient GOMAXPROCS or GOFLAGS bound, which the
// env default never does. The cap only ever narrows: an environment
// already carrying a narrower GOMAXPROCS keeps it - overriding would
// raise the operator's own bound (REQ-exec-oracle-parallelism). When
// it narrows, the cap REPLACES every ambient GOMAXPROCS entry rather
// than appending a winning duplicate: the environment is also the
// producer env gofresh records as evidence, and gofresh refuses a
// duplicate key, so a run under an exported wider GOMAXPROCS (an
// operator's shell, or a gomutant oracle measuring a gomutant run)
// would otherwise skip every target as evidence-unavailable. An
// ambient narrower value is kept as ONE entry, composed through the
// policy's one setter, so the recorded env carries the key once
// whichever side wins (gotool.SetEnv; a duplicated ambient key is
// refused at preparation, never composed around —
// REQ-exec-spawn-environment).
func oracleCPUEnv(env []string, width int) []string {
	if width <= 0 {
		return env
	}
	effective := width
	if ambient, ok := envGOMAXPROCS(env); ok && ambient <= width {
		effective = ambient
	}
	return gotool.SetEnv(env, "GOMAXPROCS", strconv.Itoa(effective))
}

// envGOMAXPROCS reports the environment's GOMAXPROCS - the entry
// naming the key under the platform's rule (a lowercase gomaxprocs is
// the same variable on Windows, another one on Unix), when well-formed
// and positive. A malformed entry reports absent, so the cap replaces
// it.
func envGOMAXPROCS(env []string) (int, bool) {
	value, ok := gotool.LookupEnv(env, "GOMAXPROCS")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n > 0
}
