package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSurfaceScanFailsClosedOnUnreadableDeclaration pins the changed-surface
// scan's mid-scan-mutation net (REQ-target-changed): a declaration the load
// parsed but whose body bytes cannot be re-read fails the scan with the
// path and declaration named — never a silent skip that would orphan the
// reference-side key and report "only deleted symbols" for code that
// exists.
func TestSurfaceScanFailsClosedOnUnreadableDeclaration(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":  "module example.com/scan\n\ngo 1.24\n",
		"lib.go":  "package scan\n\nfunc Kept() int { return 1 }\n",
		"gone.go": "package scan\n\nfunc Doomed() int { return 2 }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(string) ([]byte, bool) { return nil, false }
	// The loaded tree still parses gone.go; removing it after the load is
	// the mid-scan tree mutation the net exists for.
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	_, err = tree.SurfaceContext(context.Background(), []string{"lib.go", "gone.go"}, ref)
	if err == nil || !strings.Contains(err.Error(), "gone.go") || !strings.Contains(err.Error(), "Doomed") {
		t.Fatalf("unreadable declaration scan = %v, want a failure naming gone.go and Doomed", err)
	}
}
