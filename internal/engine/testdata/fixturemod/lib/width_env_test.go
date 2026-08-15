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
// Weak's findings. The purity assertion is a fixture workaround, not
// the target state: the package's observability proof still refuses
// at the scan tier (a sibling fixture's crypto/rand.Read - the rung
// after unsafe.Pointer in the scan-precision ladder), so the
// observed-discharge serving chain cannot carry a non-pure oracle in
// this package yet; the runtime-digest comparison this row pins runs
// for pure subjects too.
//
//gofresh:pure
func TestWeakUnderWidthEnv(t *testing.T) {
	_ = os.Getenv("GOMAXPROCS")
	if Weak(7) != 7 {
		t.Fatal("small arm")
	}
}
