package lib_test

import (
	"fmt"
	"os"
	"testing"
)

// TestExt lives in the external test package.
func TestExt(t *testing.T) {}

// TestMain is a transparent harness wrapper: never part of a derived
// oracle — it is the harness, not a test.
func TestMain(m *testing.M) {
	marker := os.Getenv("GOMUTANT_UNSTABLE_COUNT")
	if marker != "" {
		exists, err := countMarkerExistsAndSet(marker)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if exists {
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}
