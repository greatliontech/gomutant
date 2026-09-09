// Package posturemod writes the module the posture tests measure: one
// package whose function-valued variable is stored through by a
// reachable setter — process-shared dynamic state no per-subject closure
// can attribute, so its records are not reusable — beside a clean one.
package posturemod

import (
	"os"
	"path/filepath"
	"testing"
)

// Write lays the module out under a fresh temporary directory and
// returns its root: shared (Read, Set, and their test), clean (Add and
// its test), and other — a test-only package whose test links clean,
// so clean.Add's derived oracle spans two packages.
func Write(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/posture\n\ngo 1.26\n",
		"shared/shared.go": `package shared

var hook = func(n int) int { return n }

func Set(f func(int) int) { hook = f }

func Read(x int) int {
	if x > 0 {
		return hook(x) + 1
	}
	return hook(0)
}
`,
		"shared/shared_test.go": `package shared

import "testing"

func TestRead(t *testing.T) {
	if Read(2) != 3 {
		t.Fatal("read")
	}
	Set(func(n int) int { return n + 10 })
	if Read(0) != 10 {
		t.Fatal("zero")
	}
}
`,
		"clean/clean.go": `package clean

func Add(a, b int) int {
	if a > b {
		return a + b
	}
	return b + a
}
`,
		"clean/clean_test.go": `package clean

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 || Add(3, 1) != 4 {
		t.Fatal("add")
	}
}
`,
		"other/other_test.go": `package other

import (
	"testing"

	"example.com/posture/clean"
)

func TestAddFromOutside(t *testing.T) {
	if clean.Add(5, 6) != 11 {
		t.Fatal("add")
	}
}
`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
