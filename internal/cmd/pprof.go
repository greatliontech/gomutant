package cmd

import (
	"net/http"
	_ "net/http/pprof"
	"os"
)

// startDebugProfiler serves net/http/pprof when GOMUTANT_PPROF names a
// listen address - the field handle for the long-silent-stretch class:
// a run that appears hung yields goroutine and CPU profiles without a
// restart or an instrumented rebuild. Best-effort and off by default;
// a failed listen is silent (the profiler must never fail a run).
func startDebugProfiler() {
	addr := os.Getenv("GOMUTANT_PPROF")
	if addr == "" {
		return
	}
	go func() {
		_ = http.ListenAndServe(addr, nil)
	}()
}
