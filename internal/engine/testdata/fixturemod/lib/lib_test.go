package lib

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("sum")
	}
	if Add(0, 5) != 5 {
		t.Fatal("zero arm")
	}
}

func TestWeak(t *testing.T) {
	if Weak(5) != 5 {
		t.Fatal("small arm")
	}
}

// TestVacuous is deliberately assertion-free: a weak always-passing oracle.
func TestVacuous(t *testing.T) {
	_ = Add(1, 2)
}

func TestGuarded(t *testing.T) {
	if Guarded(3) != 6 {
		t.Fatal("broken")
	}
}

// Testhelper is not a runnable test — the lowercase continuation and the
// non-harness signature mean go test never runs it — so it must never enter
// a derived oracle.
func Testhelper(n int) int { return n }

// FuzzAdd's seed corpus runs in an ordinary go test invocation, so it is
// part of the package's derived oracle.
func FuzzAdd(f *testing.F) {
	f.Add(1, 2)
	f.Fuzz(func(t *testing.T, a, b int) {
		if Add(a, b) != Add(b, a) {
			t.Fatal("Add not commutative")
		}
	})
}
