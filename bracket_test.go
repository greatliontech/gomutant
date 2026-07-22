package gomutant

import (
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// testBracket captures an observation bracket over the whole root so
// direct testlog constructions satisfy the completed-observation
// contract exactly as the engine's pre-spawn capture does.
func testBracket(t *testing.T, root string) runtimeinput.Bracket {
	t.Helper()
	b, err := runtimeinput.CaptureBracket(root, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
