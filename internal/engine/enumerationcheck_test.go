package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyTestEnumerationDetectsLag pins the enumeration-freshness
// cross-check: a loaded snapshot that lags the on-disk test files — a test
// appended to an existing file, or a whole new test file — refuses with the
// differing identities named in either direction, while a fresh snapshot,
// aliased and dot-imported testing packages, and constraint-excluded files
// all verify clean.
func TestVerifyTestEnumerationDetectsLag(t *testing.T) {
	dir := t.TempDir()
	const pkg = "example.com/lag"
	files := map[string]string{
		"go.mod": "module example.com/lag\n\ngo 1.26\n",
		"p.go":   "package p\n\nfunc F() int { return 1 }\n",
		// The baseline exercises the syntactic predicate's import handling:
		// an aliased testing import and a dot import must both count.
		"p_test.go":   "package p\n\nimport (\n\tte \"testing\"\n\t\"testing\"\n)\n\nfunc TestF(_ *te.T) {\n\tif F() != 1 {\n\t\tpanic(\"broken\")\n\t}\n}\n\nfunc TestPlain(_ *testing.T) {}\n\nfunc helper() {}\n",
		"dot_test.go": "package p\n\nimport . \"testing\"\n\nfunc TestDot(_ *T) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	verifyAt := func(tree *Tree, pkgPath string) error {
		derived, derr := tree.TestsOfContext(ctx, pkgPath)
		if derr != nil {
			t.Fatal(derr)
		}
		return tree.VerifyTestEnumerationContext(ctx, pkgPath, derived)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAt(tr, pkg); err != nil {
		t.Fatalf("fresh snapshot refused: %v", err)
	}

	// Content lag: a test appended on disk after the load.
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(files["p_test.go"]+"\nfunc TestNew(_ *te.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = verifyAt(tr, pkg)
	if err == nil || !strings.Contains(err.Error(), "lags the tree") || !strings.Contains(err.Error(), "on disk but not enumerated: example.com/lag.TestNew") {
		t.Fatalf("content lag = %v, want the missing identity named", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAt(reloaded, pkg); err != nil {
		t.Fatalf("reloaded snapshot refused: %v", err)
	}

	// New-file lag: a whole test file the snapshot never loaded.
	if err := os.WriteFile(filepath.Join(dir, "extra_test.go"), []byte("package p\n\nimport \"testing\"\n\nfunc TestExtra(_ *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = verifyAt(reloaded, pkg)
	if err == nil || !strings.Contains(err.Error(), "example.com/lag.TestExtra") {
		t.Fatalf("new-file lag = %v, want the missing identity named", err)
	}
	if err := os.Remove(filepath.Join(dir, "extra_test.go")); err != nil {
		t.Fatal(err)
	}

	// A constraint-excluded file is not part of this test binary: no lag.
	if err := os.WriteFile(filepath.Join(dir, "excluded_test.go"), []byte("//go:build neverbuildme\n\npackage p\n\nimport \"testing\"\n\nfunc TestExcluded(_ *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyAt(reloaded, pkg); err != nil {
		t.Fatalf("constraint-excluded file read as lag: %v", err)
	}

	// Removal lag: a test the snapshot still carries no longer exists on
	// disk — serving against it would trust an oracle no test backs.
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte(files["p_test.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	err = verifyAt(reloaded, pkg)
	if err == nil || !strings.Contains(err.Error(), "enumerated but not on disk: example.com/lag.TestNew") {
		t.Fatalf("removal lag = %v, want the vanished identity named", err)
	}

	// Constraint-edit lag: excluding an existing file by a build constraint
	// after the load is a lag too — the stale snapshot still counts its
	// tests while the test binary excludes them.
	current, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAt(current, pkg); err != nil {
		t.Fatalf("reloaded snapshot refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dot_test.go"), []byte("//go:build neverbuildme\n\n"+files["dot_test.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	err = verifyAt(current, pkg)
	if err == nil || !strings.Contains(err.Error(), "enumerated but not on disk: example.com/lag.TestDot") {
		t.Fatalf("constraint-edit lag = %v, want the excluded identity named", err)
	}
}

// TestVerifyTestEnumerationHonorsEffectiveBuildTags pins the effective-config
// constraint evaluation: a new test file selected only by a GOFLAGS tag —
// the double-dash form the go command equally accepts — is lag under that
// configuration and constraint-excluded noise without it.
func TestVerifyTestEnumerationHonorsEffectiveBuildTags(t *testing.T) {
	t.Setenv("GOFLAGS", "--tags=integrationlag")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/tagged\n\ngo 1.26\n",
		"p.go":      "package p\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestF(_ *testing.T) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := tr.TestsOfContext(ctx, "example.com/tagged")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tagged_test.go"), []byte("//go:build integrationlag\n\npackage p\n\nimport \"testing\"\n\nfunc TestTagged(_ *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = tr.VerifyTestEnumerationContext(ctx, "example.com/tagged", derived)
	if err == nil || !strings.Contains(err.Error(), "example.com/tagged.TestTagged") {
		t.Fatalf("tag-selected new file = %v, want lag named under the effective configuration", err)
	}
}

// TestBuildMatchContextResolvesEffectiveConfig pins the matcher derivation:
// a repeated -tags is last-wins exactly as the go command resolves it, and
// release tags follow the tree's toolchain version.
func TestBuildMatchContextResolvesEffectiveConfig(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=first --tags=second,third")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/cfg\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := tr.buildMatchContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(matcher.BuildTags) != 2 || matcher.BuildTags[0] != "second" || matcher.BuildTags[1] != "third" {
		t.Fatalf("build tags = %v, want the last -tags occurrence only", matcher.BuildTags)
	}
	if len(matcher.ReleaseTags) < 26 || matcher.ReleaseTags[0] != "go1.1" {
		t.Fatalf("release tags = %v, want the toolchain-derived ladder", matcher.ReleaseTags)
	}
}
