package gomutant

import (
	"testing"

	"github.com/greatliontech/gofresh/shortgates"
)

// The fast tier's gates are the partition the default build selection
// runs by (internal/integrationtest runs a gate-carrying suite as -short
// unless told otherwise): every gate in the repository's test files is
// a skipping statement of a Test or Fuzz body, never in a helper, an
// uninvoked closure, or a fixture string
// (github.com/greatliontech/gofresh/shortgates states the rules and pins
// its own arms).
func TestShortGatesLiveInTestBodiesAndSkip(t *testing.T) {
	shortgates.Pin(t, ".")
}
