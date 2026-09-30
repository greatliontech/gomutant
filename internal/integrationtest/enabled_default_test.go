//go:build !integration

package integrationtest

import "testing"

// The constant is pinned from outside itself: a test file compiled only
// without the tag knows the selection it was built under.
func TestDefaultSelectionIsNotIntegration(t *testing.T) {
	if Enabled {
		t.Fatal("Enabled is true under the default build selection")
	}
}
