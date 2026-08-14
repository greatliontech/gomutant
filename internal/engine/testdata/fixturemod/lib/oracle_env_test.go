package lib

import (
	"os"
	"testing"
)

// TestOracleEnvHasParallelismCap exists for oracle-environment testing:
// when the spawning harness requests the assertion, it fails exactly
// when that harness delivered no inner-parallelism cap, so a harness
// test selecting it can tell a capped spawn from an uncapped one on
// both the mutant and the baseline-probe paths. Without the request it
// skips, so incidental selections (campaign oracles, discovery probes)
// are unaffected.
func TestOracleEnvHasParallelismCap(t *testing.T) {
	if os.Getenv("FIXTURE_REQUIRE_PARALLELISM_CAP") == "" {
		t.Skip("cap assertion not requested")
	}
	if os.Getenv("GOMAXPROCS") == "" {
		t.Fatal("no inner-parallelism cap in the oracle environment")
	}
}
