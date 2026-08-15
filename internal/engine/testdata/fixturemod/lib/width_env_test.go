package lib

import (
	"os"
	"testing"
)

// TestWeakUnderWidthEnv exists for oracle-evidence testing: it reads
// the delivered GOMAXPROCS - putting an environment identity into
// every observation that selects it - and exercises Weak's small arm,
// so Weak's discovered oracle set carries a width-reading member and
// serve-side revalidation must reproduce the recorded width to reuse
// Weak's findings. The purity assertion mirrors the package's other
// oracle fixtures: the runtime-digest comparison runs for pure
// subjects too, which is exactly the arm this fixture exists to pin.
//
//gofresh:pure
func TestWeakUnderWidthEnv(t *testing.T) {
	_ = os.Getenv("GOMAXPROCS")
	if Weak(7) != 7 {
		t.Fatal("small arm")
	}
}
