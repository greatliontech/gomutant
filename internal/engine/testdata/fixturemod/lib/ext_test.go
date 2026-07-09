package lib_test

import (
	"os"
	"testing"
)

// TestExt lives in the external test package.
func TestExt(t *testing.T) {}

// TestMain is a transparent harness wrapper: never part of a derived
// oracle — it is the harness, not a test.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
