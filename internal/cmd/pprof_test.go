package cmd

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// The field handle for the long-silent-stretch class: with
// GOMUTANT_PPROF set, the profiler serves without a restart or an
// instrumented rebuild.
func TestDebugProfilerServesWhenConfigured(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	t.Setenv("GOMUTANT_PPROF", addr)
	startDebugProfiler()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/", addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("profiler never served /debug/pprof/")
}
