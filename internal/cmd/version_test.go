package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	gomutant "github.com/greatliontech/gomutant"
)

// Both version surfaces state the binary identity and the findings
// document versions - the capability line a version-skew field report
// needs (a long-lived server older than the CLI refuses the document
// by exactly this range).
func TestVersionSurfaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("malformed module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PATH", t.TempDir())
	prior := seams
	t.Cleanup(func() { seams = prior })
	seams.memoryLimitInstalled = func(int64) { t.Error("version installed execution policy") }
	want := fmt.Sprintf("findings document version %d, reads %d-%d", gomutant.DocumentVersion, gomutant.OldestReadableDocumentVersion, gomutant.DocumentVersion)
	if got := versionString(); !strings.Contains(got, want) {
		t.Fatalf("versionString() = %q, want it to carry %q", got, want)
	}
	for _, args := range [][]string{{"version"}, {"--version"}} {
		var buf bytes.Buffer
		cmd := newRootCommand()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("%v output = %q, want the document-version line", args, buf.String())
		}
		for _, field := range []string{"revision=", "modified=", "go=", "gofresh="} {
			if !strings.Contains(buf.String(), field) {
				t.Fatalf("missing %s: %q", field, buf.String())
			}
		}
	}
	cmd := newRootCommand()
	cmd.SetArgs([]string{"version", "unexpected"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("version accepted an operand")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Fatalf("query wrote state: %v, %v", entries, err)
	}
}

func TestVersionExecutableNeedsNoGoOrGit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := filepath.Join(t.TempDir(), "gomutant")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/greatliontech/gomutant/cmd/gomutant").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}, {"--help"}} {
		cmd := exec.CommandContext(t.Context(), bin, args...)
		cmd.Dir = dir
		cmd.Env = gotool.SetEnv(os.Environ(), "PATH", "")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "version") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("query wrote state: %v, %v", entries, err)
	}
}
