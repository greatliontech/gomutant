package flakyattr

import (
	"os"
	"testing"
)

func count(t *testing.T, name string) {
	if path := os.Getenv("GOMUTANT_EXECUTION_COUNTER"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(name + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAWeak sorts ahead of the killer and never fails: the full
// serial oracle's fallback is observable through its counter line —
// a killer-scoped run never executes it.
func TestAWeak(t *testing.T) { count(t, "TestAWeak"); _ = Value() }

func TestFlakyAttr(t *testing.T) {
	count(t, "TestFlakyAttr")
	if Value() == 7 {
		return
	}
	marker := os.Getenv("GOMUTANT_FLAKYATTR_MARKER")
	if marker == "" {
		t.Fatal("mutated without a marker path")
	}
	if _, err := os.Stat(marker); err == nil {
		return // second look: the failure does not reproduce
	}
	if err := os.WriteFile(marker, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The first look fails CLEANLY: an ordinary attributed test
	// failure, so confirmation enters the killer-scoped stage.
	t.Fatal("first look: attributed interference-shaped failure")
}
