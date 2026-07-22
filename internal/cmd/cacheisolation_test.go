package cmd

import (
	"os"
	"testing"
)

// TestMain isolates the machine-local findings overlay: tests must
// never write the developer's real user cache, and cross-run overlay
// leakage would shadow fixture findings with stale entries.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "gomutant-test-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CACHE_HOME", tmp)
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}
