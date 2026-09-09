// Package gitfixture builds the committed-plus-edited module both
// faces' changed-ref tests run over: one function with an uncommitted
// edit adding a branch on lines 4-6, so a changed-ref run cuts open
// survivors by the delta. Test support only.
package gitfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
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
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write("p.go", "package dl\n\nfunc Value(x int) int {\n\tif x < -10 {\n\t\treturn 3\n\t}\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	return dir
}
