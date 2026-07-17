package candlocal

import "testing"

func TestValue(t *testing.T) {
	got := Value(2)
	if got == 0 {
		panic("zero mutant")
	}
	if got != 3 {
		t.Fatal("wrong value")
	}
}
