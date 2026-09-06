package g

import "testing"

func TestCheck(t *testing.T) {
	if Check(1) != nil {
		t.Fatal("positive refused")
	}
	if Check(-1) == nil {
		t.Fatal("negative accepted")
	}
}
