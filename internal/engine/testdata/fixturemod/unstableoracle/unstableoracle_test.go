package unstableoracle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStable(t *testing.T) {
	if Value() != 1 {
		t.Fatal("value")
	}
}

func TestUnstable(t *testing.T) {
	scratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.WriteFile(scratch, []byte("ephemeral"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(scratch); err != nil {
		t.Fatal(err)
	}
	if Value() != 1 {
		t.Fatal("value")
	}
}

func TestWeakly(t *testing.T) {
	if Weakly(3) != 3 {
		t.Fatal("weakly")
	}
}
