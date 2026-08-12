package lib_test

import (
	"os"
	"testing"
)

// TestExt lives in the external test package.
func TestExt(t *testing.T) {}

// TestMain is a transparent harness wrapper: never part of a derived
// oracle — it is the harness, not a test. It stays effect-free so the
// binary's test-main flow proves observable; the count-drift marker
// machinery lives in the unstable fixture package.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
