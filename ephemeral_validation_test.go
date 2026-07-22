package gomutant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ephemeral runs refuse inputs the build would silently ignore
// (REQ-exec-ephemeral): a test package that is not a loaded import path
// (a "-exec=..." value would otherwise parse as a go test flag), and a
// replacement of a file the loaded build does not compile (a
// build-excluded source or a data file), whose mutation could never be
// exercised - the run would report a false survivor.
func TestEphemeralRefusesUnloadedPackageAndUncompiledFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "excluded.go"), []byte("//go:build never\n\npackage lib\n\nfunc Excluded() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "data.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	valid := []byte("package lib\n")

	if _, err := tr.Ephemeral(t.Context(), "lib/excluded.go", []byte("//go:build never\n\npackage lib\n\nfunc Excluded() int { return 2 }\n"), "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "not compiled by the loaded build") {
		t.Fatalf("build-excluded replacement = %v, want an exclusion refusal", err)
	}
	if _, err := tr.Ephemeral(t.Context(), "lib/data.txt", []byte("mutated"), "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "not compiled by the loaded build") {
		t.Fatalf("data-file replacement = %v, want an exclusion refusal", err)
	}
	if _, err := tr.Ephemeral(t.Context(), "lib/lib.go", valid, "-exec=/bin/true", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "not a loaded package import path") {
		t.Fatalf("flag-shaped test package = %v, want a loaded-package refusal", err)
	}
	if _, err := tr.Ephemeral(t.Context(), "lib/lib.go", valid, "example.com/nowhere", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "not a loaded package import path") {
		t.Fatalf("unloaded test package = %v, want a loaded-package refusal", err)
	}
}
