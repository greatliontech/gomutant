// Package failing has a test that fails on the clean tree: the probe must
// report it so a caller never scores a mutant against it.
package failing

import "testing"

func TestAlwaysFails(t *testing.T) {
	t.Error("fails on the clean tree by design")
}
