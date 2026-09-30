package gomutant

import (
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/testsuite"
)

// TestMain is the repository's one suite entry (internal/testsuite):
// the overlay isolated, the selection's -short default taken, the
// tracked fixture trees guarded.
func TestMain(m *testing.M) {
	testsuite.Main(m, filepath.Dir(fixtureDir))
}
