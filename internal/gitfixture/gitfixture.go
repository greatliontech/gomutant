// Package gitfixture builds the committed-plus-edited module both
// faces' changed-ref tests run over: one function with an uncommitted
// edit adding a branch on lines 4-6, so a changed-ref run cuts open
// survivors by the delta — and holds the one git command a fixture
// repository is built with (Command). Test support only.
package gitfixture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/gitcmd"
)

// Command is git over a fixture repository at dir under the hermetic
// environment (gitcmd.Hermetic over the process's: the host's
// configuration scopes, command-scope entries and repository
// redirection closed — an older git than 2.32 would inherit the global
// configuration, which the hermetic pin then names), so no hook path,
// file-system monitor, signing rule, or maintenance setting of the
// host reaches the repository — a writer the host forks into a
// temporary tree would race its removal — and the identity and the
// no-maintenance rule are the repository's own (Init). A fixture
// repository is built with it alone, over the one git runner
// (internal/gitcmd).
func Command(dir string, args ...string) (*exec.Cmd, error) {
	return gitcmd.Prepare(context.Background(), dir, gitcmd.Hermetic(os.Environ()), args...)
}

// Init initialises a fixture repository at dir: no background
// maintenance — a commit would otherwise fork `git maintenance run
// --auto` into the tree after the test moved on (maintenance.auto
// gates every automatic caller on git 2.29 and later; gc.auto 0 is the
// belt for the direct `gc --auto` of an older git) — and a fixed
// identity, so a commit needs nothing of the host's.
func Init(dir string) error {
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "maintenance.auto", "false"},
		{"config", "gc.auto", "0"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
	} {
		cmd, err := Command(dir, args...)
		if err != nil {
			return fmt.Errorf("git %v: %w", args, err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
	}
	return nil
}

// Changed returns the fixture's root, skipping the test when git is
// unavailable.
func Changed(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd, err := Command(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/dl\n\ngo 1.26.5\n")
	write("p.go", "package dl\n\nfunc Value(x int) int {\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	write("p_test.go", "package dl\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value(0) != 2 {\n\t\tt.Fatal(Value(0))\n\t}\n}\n")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write("p.go", "package dl\n\nfunc Value(x int) int {\n\tif x < -10 {\n\t\treturn 3\n\t}\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	return dir
}
