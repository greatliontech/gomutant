package plain

import "testing"

// TestPlain always passes and touches nothing outside this package.
func TestPlain(t *testing.T) {
	if !Ok() {
		t.Fatal("broken")
	}
}
