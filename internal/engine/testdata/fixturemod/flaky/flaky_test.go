package flaky

import (
	"os"
	"testing"
)

func TestFlaky(t *testing.T) {
	if Value() == 7 {
		return
	}
	marker := os.Getenv("GOMUTANT_FLAKY_MARKER")
	if marker == "" {
		t.Fatal("mutated without a marker path")
	}
	if _, err := os.Stat(marker); err == nil {
		return // second look: the failure does not reproduce
	}
	if err := os.WriteFile(marker, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The first look dies the way interference does: a goroutine panic
	// crashing the binary, so the concurrent kill carries incomplete
	// observation evidence a clean serial run must fully replace.
	done := make(chan struct{})
	go func() {
		panic("first look interference")
	}()
	<-done
}
