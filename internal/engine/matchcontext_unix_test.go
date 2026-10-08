//go:build unix

package engine

import (
	"context"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// goVersionShim puts a `go` ahead of the unshimmed PATH — the program
// resolution rule — that answers `go env -json` with the real go's
// document but GOVERSION rewritten to the version given (and `go env
// GOVERSION` with it), forwarding everything else to the real go; an
// empty version restores the unshimmed PATH.
func goVersionShim(t *testing.T, real, path, version string) {
	t.Helper()
	if version == "" {
		t.Setenv("PATH", path)
		return
	}
	shims := t.TempDir()
	shim := "#!/bin/sh\n" +
		"if [ \"$1\" = env ] && [ \"$2\" = -json ]; then " + real + " \"$@\" | sed 's/\"GOVERSION\": \"[^\"]*\"/\"GOVERSION\": \"" + version + "\"/'; exit $?; fi\n" +
		"if [ \"$1\" = env ] && [ \"$2\" = GOVERSION ] && [ $# = 2 ]; then echo '" + version + "'; exit 0; fi\n" +
		"exec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shims, "go"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+path)
}

// The matcher's release-tag ladder is the TREE's toolchain's, read from
// the env snapshot the tree's go command answers — never the host
// defaults of the toolchain that compiled this binary: a go whose
// snapshot names go1.40rc1 (a shim installed after the load, which the
// real go served) installs the forty-tag ladder ending at go1.40, where
// the host's defaults end at its own series; a snapshot whose GOVERSION
// the grammar refuses keeps the host's defaults. End to end: a
// `//go:build go1.40` test file that go1.40rc1 selects and the loader's
// toolchain did not is exactly the lag the cross-check refuses, naming
// the test (REQ-target-default).
func TestMatchContextReleaseTagsFollowTheTreeToolchain(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture trees")
	}
	ctx := context.Background()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	write := func(dir string, files map[string]string) {
		t.Helper()
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	base := map[string]string{
		"go.mod":    "module example.com/cfg\n\ngo 1.26\n",
		"p.go":      "package p\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package p\n\nimport \"testing\"\n\nfunc TestF(_ *testing.T) {}\n",
	}
	// The ladder follows the snapshot's version.
	dir := t.TempDir()
	write(dir, base)
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	goVersionShim(t, real, path, "go1.40rc1")
	matcher, err := tr.buildMatchContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(matcher.ReleaseTags) != 40 || matcher.ReleaseTags[0] != "go1.1" || matcher.ReleaseTags[39] != "go1.40" {
		t.Fatalf("release tags under go1.40rc1 = %v; want the ladder go1.1 … go1.40, never the host's", matcher.ReleaseTags)
	}
	// A version the grammar refuses keeps the host's defaults.
	refused := t.TempDir()
	write(refused, base)
	goVersionShim(t, real, path, "")
	tr2, err := Load(refused)
	if err != nil {
		t.Fatal(err)
	}
	goVersionShim(t, real, path, "devel +abc123")
	matcher, err = tr2.buildMatchContext(ctx, refused)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(matcher.ReleaseTags, build.Default.ReleaseTags) {
		t.Fatalf("release tags under a refused GOVERSION = %v; want the host's defaults %v", matcher.ReleaseTags, build.Default.ReleaseTags)
	}
	// End to end: a go1.40-constrained test file is lag under go1.40rc1.
	lag := t.TempDir()
	write(lag, base)
	goVersionShim(t, real, path, "")
	write(lag, map[string]string{"future_test.go": "//go:build go1.40\n\npackage p\n\nimport \"testing\"\n\nfunc TestFuture(_ *testing.T) {}\n"})
	tr3, err := Load(lag)
	if err != nil {
		t.Fatal(err)
	}
	goVersionShim(t, real, path, "go1.40rc1")
	derived, err := tr3.TestsOfContext(ctx, "example.com/cfg")
	if err != nil {
		t.Fatal(err)
	}
	err = tr3.VerifyTestEnumerationContext(ctx, "example.com/cfg", derived)
	if err == nil || !strings.Contains(err.Error(), "on disk but not enumerated: example.com/cfg.TestFuture") {
		t.Fatalf("the cross-check under go1.40rc1 = %v; want the lag refusal naming TestFuture as on disk but not enumerated", err)
	}
}
