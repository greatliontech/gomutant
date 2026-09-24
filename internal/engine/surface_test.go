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

// A file's surface carries the package it belongs to: the loaded
// package holding it, a test file's under its base package; for a file
// the loaded packages do not hold, the package holding its directory,
// else the directory's path under its main module; none outside every
// main module (REQ-target-changed).
func TestSurfaceCarriesEachFilesPackage(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/scan\n\ngo 1.24\n",
		"lib.go":        "package scan\n\nfunc Kept() int { return 1 }\n",
		"lib_test.go":   "package scan\n\nimport \"testing\"\n\nfunc TestKept(t *testing.T) { if Kept() != 1 { t.Fatal() } }\n",
		"sub/s.go":      "package sub\n\nfunc S() int { return 2 }\n",
		"sub/s_test.go": "package sub_test\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) {}\n",
		"nested/go.mod": "module example.com/other\n\ngo 1.24\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(string) ([]byte, bool) { return nil, false }
	surface, err := tree.SurfaceContext(context.Background(), []string{"lib.go", "lib_test.go", "sub/s_test.go", "gone_test.go", "sub/gone_test.go", "removed/r_test.go", "notes.txt", "../outside_test.go", "nested/n_test.go", "nested/deep/d_test.go", "testdata/t_test.go", "sub/_skip/s_test.go", ".hidden/h_test.go"}, ref)
	if err != nil {
		t.Fatal(err)
	}
	// gone_test.go and sub/gone_test.go: deleted files under directories
	// loaded packages hold; removed/r_test.go: a deleted package, its
	// directory's path under the module; notes.txt: no Go file; a path
	// outside the module, under a nested module (at its root or
	// deeper), under testdata, or under an element beginning with "_"
	// or ".": no package.
	want := map[string]string{"lib.go": "example.com/scan", "lib_test.go": "example.com/scan", "sub/s_test.go": "example.com/scan/sub",
		"gone_test.go": "example.com/scan", "sub/gone_test.go": "example.com/scan/sub", "removed/r_test.go": "example.com/scan/removed", "notes.txt": "", "../outside_test.go": "",
		"nested/n_test.go": "", "nested/deep/d_test.go": "", "testdata/t_test.go": "", "sub/_skip/s_test.go": "", ".hidden/h_test.go": ""}
	for _, fs := range surface {
		if fs.Package != want[fs.Path] {
			t.Fatalf("%s: package %q, want %q", fs.Path, fs.Package, want[fs.Path])
		}
	}
}
