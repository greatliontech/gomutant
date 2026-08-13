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
	// An absolute external read - under the user home, outside the
	// module and the minted TMPDIR - that no bracket covers and no
	// declared root admits: this run's completed evidence stays
	// content-unverifiable by contract on every platform. Minted-TMPDIR
	// scratch no longer serves as the vehicle - ingest declares the
	// tool's scratch root as an ephemeral temp root and swept reads
	// admit recordless. The file's absence is immaterial: the open
	// intent records.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.ReadFile(filepath.Join(home, ".gomutant-external-input-fixture"))
	if Value() != 1 {
		t.Fatal("value")
	}
}

func TestWeakly(t *testing.T) {
	if Weakly(3) != 3 {
		t.Fatal("weakly")
	}
}
