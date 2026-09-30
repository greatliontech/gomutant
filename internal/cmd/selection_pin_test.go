package cmd

import (
	"testing"

	"github.com/greatliontech/gomutant/internal/integrationtest"
)

// The suite takes the build selection's default (its TestMain is
// testsuite.Main, which calls integrationtest.DefaultToShort): under
// the default selection this binary is the unit suite gomutant's own
// campaigns derive as their oracle; under -tags integration every test
// runs.
func TestSuiteTakesTheSelectionsDefault(t *testing.T) {
	integrationtest.Pin(t)
}
