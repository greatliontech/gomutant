package diskread

import (
	"os"
	"strings"
	"testing"
)

// TestDiskVerdict reads the mutated file's on-disk bytes - the
// overlay-bypass shape: the mutant links into the binary, but the
// verdict-bearing read sees the unmutated tree.
func TestDiskVerdict(t *testing.T) {
	source, err := os.ReadFile("disk.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "func D(") {
		t.Fatal("source scan failed")
	}
	if D(5) != 5 {
		t.Fatal("small arm")
	}
}
