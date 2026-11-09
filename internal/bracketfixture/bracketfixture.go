// Package bracketfixture captures an observation bracket for tests
// that construct runtime-input observations directly: the bracket over
// the whole root satisfies the completed-observation contract exactly
// as the engine's pre-spawn capture does.
package bracketfixture

import (
	"testing"

	"github.com/greatliontech/gofresh"
)

// Capture is the bracket over root's whole tree, under the test's own
// context: called from the test body, never from a cleanup, whose
// context is already cancelled.
func Capture(t testing.TB, root string) gofresh.Bracket {
	t.Helper()
	b, err := gofresh.CaptureBracket(t.Context(), root, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
