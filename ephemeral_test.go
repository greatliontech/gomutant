package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEphemeral pins the manual-mutant runner (REQ-exec-ephemeral): a
// behavior-breaking replacement is killed with an attributed killer, a
// replacement the test cannot see survives, an identical replacement and a
// zero-match or failing-clean probe refuse the run, and the working tree is
// never touched.
func TestEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("GOMUTANT_FROZEN_INPUT", "loaded")
	tr := fixtureTree(t)
	t.Setenv("GOMUTANT_FROZEN_INPUT", "changed-after-load")
	ctx := context.Background()
	libPath := filepath.Join(fixtureDir, "lib", "lib.go")
	orig, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(fixtureDir, "lib", "doc.go")
	origDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}

	// Breaking Add's tested arm: TestAdd kills, attributed.
	broken := strings.Replace(string(orig), "return a + b", "return a + b + 1", 1)
	res, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestAdd$", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("breaking mutant = %+v, want killed by TestAdd", res)
	}
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestFrozenEnvironment$", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestFrozenEnvironment" {
		t.Fatalf("frozen-environment mutant = %+v, want attributed kill", res)
	}

	// Breaking only Weak's untested branch: TestWeak cannot see it.
	unseen := strings.Replace(string(orig), "return x - 1", "return x - 2", 1)
	res, err = tr.Ephemeral(ctx, "lib/lib.go", []byte(unseen), "example.com/fixture/lib", "^TestWeak$", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("unseen mutant = %+v, want survivor", res)
	}

	// Refusals: identical content, a pattern matching nothing, a test
	// failing on the clean tree.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", orig, "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("identical replacement scored: %v", err)
	}
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/lib", "^TestNoSuch$", time.Minute); err == nil || !strings.Contains(err.Error(), "matched no tests") {
		t.Fatalf("zero-match probe scored: %v", err)
	}
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte(broken), "example.com/fixture/failing", "^TestAlwaysFails$", time.Minute); err == nil || !strings.Contains(err.Error(), "does not pass on the unmutated tree") {
		t.Fatalf("failing-clean probe scored: %v", err)
	}

	// A replacement that does not compile measured nothing: an error, never
	// a survivor.
	if _, err := tr.Ephemeral(ctx, "lib/lib.go", []byte("package lib\nfunc Broken( {"), "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || !strings.Contains(err.Error(), "did not compile") {
		t.Fatalf("uncompilable replacement scored: %v", err)
	}

	// The edits form measures identically to the whole replacement
	// (REQ-exec-ephemeral): state the change, not the file.
	res, err = tr.EphemeralEdits(ctx, "lib/lib.go", []Edit{{Old: "return a + b", New: "return a + b + 1"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("edits mutant = %+v, want killed by TestAdd", res)
	}
	if _, err := tr.EphemeralEdits(ctx, "lib/lib.go", []Edit{{Old: "no such text", New: "x"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil {
		t.Fatal("zero-match edit scored")
	}
	res, err = tr.EphemeralBatch(ctx, []BatchEdit{
		{File: "lib/lib.go", OldString: "return a + b", NewString: "return a + b + manualDelta()"},
		{File: "lib/doc.go", OldString: "package lib", NewString: "package lib\n\nfunc manualDelta() int { return 1 }"},
	}, "example.com/fixture/lib", "^TestAdd$", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || len(res.Files) != 2 || res.Files[0] != "lib/doc.go" || res.Files[1] != "lib/lib.go" {
		t.Fatalf("multi-file edit batch = %+v", res)
	}

	// The tree was never touched.
	after, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(orig) {
		t.Fatal("the working tree was modified")
	}
	afterDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterDoc) != string(origDoc) {
		t.Fatal("the secondary overlaid file was modified")
	}
}

func TestEphemeralRejectsEscapingFiles(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "module")
	if err := os.CopyFS(root, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"../outside.go", outside, `C:\outside.go`, "link.go"} {
		if _, err := tree.Ephemeral(context.Background(), file, []byte("package changed\n"), "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || (!strings.Contains(err.Error(), "tree-relative") && !strings.Contains(err.Error(), "escapes")) {
			t.Fatalf("whole replacement accepted %q: %v", file, err)
		}
		if _, err := tree.EphemeralEdits(context.Background(), file, []Edit{{Old: "package", New: "package"}}, "example.com/fixture/lib", "^TestAdd$", time.Minute); err == nil || (!strings.Contains(err.Error(), "tree-relative") && !strings.Contains(err.Error(), "escapes")) {
			t.Fatalf("sequential edits accepted %q: %v", file, err)
		}
	}
}
