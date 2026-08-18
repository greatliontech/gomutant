package orderdep

import (
	"os"
	"testing"
)

var ready bool

// TestASetup sorts first and arms the order-dependent killer.
func TestASetup(t *testing.T) { ready = true }

func TestOrderDep(t *testing.T) {
	if !ready {
		t.Fatal("setup did not run") // fails standalone, mutant or not
	}
	if Value() == 7 {
		return
	}
	marker := os.Getenv("GOMUTANT_ORDERDEP_MARKER")
	if marker == "" {
		t.Fatal("mutated without a marker path")
	}
	if _, err := os.Stat(marker); err == nil {
		return // second look: the failure does not reproduce
	}
	if err := os.WriteFile(marker, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Fatal("first look: sibling-shaped false kill")
}
