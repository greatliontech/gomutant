package sub

import "testing"

func TestNested(t *testing.T) {
	if Nested() != 2 {
		t.Fatal("broken")
	}
}
