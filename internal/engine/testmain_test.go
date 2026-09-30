package engine

import (
	"testing"

	"github.com/greatliontech/gomutant/internal/testsuite"
)

// TestMain is the repository's one suite entry (internal/testsuite):
// the overlay isolated, the selection's -short default taken, the
// tracked fixture trees guarded — the tests that plant a file inside
// the fixture module do so in a copy (copiedFixture).
func TestMain(m *testing.M) {
	testsuite.Main(m, "testdata")
}
