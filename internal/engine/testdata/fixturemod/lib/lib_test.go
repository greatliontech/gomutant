package lib

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

//gofresh:pure
func TestPickInput(t *testing.T) {
	got := PickInput()
	_, _ = os.ReadFile(fmt.Sprintf("input-%d.txt", got))
	if got != 1 {
		t.Fatalf("PickInput() = %d", got)
	}
}

func TestMovingInput(t *testing.T) {
	path := os.Getenv("GOMUTANT_MOVING_INPUT")
	if path == "" {
		t.Skip("mutation-run fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "A" {
		if err := os.WriteFile(path, []byte("B"), 0o644); err != nil {
			t.Fatal(err)
		}
		if Add(1, 2) != 3 {
			t.Fatal("sum")
		}
	}
}

func TestUnstableInput(t *testing.T) {
	path := os.Getenv("GOMUTANT_UNSTABLE_INPUT")
	if path == "" {
		t.Skip("baseline-run fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := []byte("A")
	if string(data) == "A" {
		next = []byte("B")
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnstableBaselineResult(t *testing.T) {
	path := os.Getenv("GOMUTANT_UNSTABLE_RESULT")
	if path == "" {
		t.Skip("baseline-run fixture")
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("second baseline fails")
	}
	if err := os.WriteFile(path, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChangingIdentity(t *testing.T) {
	stable := os.Getenv("GOMUTANT_STABLE_INPUT")
	if stable == "" {
		t.Skip("baseline-run fixture")
	}
	if _, err := os.ReadFile(stable); err != nil {
		t.Fatal(err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(".", fmt.Sprintf(".changing-identity-%x", nonce))
	defer os.Remove(path)
	if err := os.WriteFile(path, []byte("generated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedFixture(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".generated-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "input")
	if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	if Add(1, 2) != 3 {
		t.Fatal("sum")
	}
}

func TestDriftSource(t *testing.T) {
	if Add(1, 2) == 3 {
		return
	}
	path := os.Getenv("GOMUTANT_DRIFT_SOURCE")
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, []byte("\n// mutant drift\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("sum")
}

func TestNamedPanic(t *testing.T) {
	if PanicValue() != 1 {
		panic("mutant changed value")
	}
}

//gofresh:pure
func TestFrozenEnvironment(t *testing.T) {
	if os.Getenv("GOMUTANT_FROZEN_INPUT") != "loaded" {
		return
	}
	if Add(1, 2) != 3 {
		t.Fatal("sum")
	}
}

//gofresh:pure
func TestAdd(t *testing.T) {
	_ = os.Getenv("GOMUTANT_TEST_INPUT")
	_ = os.Getenv("GOWORK")
	if Add(1, 2) != 3 {
		t.Fatal("sum")
	}
	if Add(0, 5) != 5 {
		t.Fatal("zero arm")
	}
}

//gofresh:pure
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
