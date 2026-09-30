//go:build integration

package integrationtest

import "testing"

// The constant is pinned from outside itself: a test file compiled only
// under the tag knows the selection it was built under.
func TestIntegrationSelectionIsIntegration(t *testing.T) {
	if !Enabled {
		t.Fatal("Enabled is false under -tags integration")
	}
}
