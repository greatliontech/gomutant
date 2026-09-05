package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The run-selectable names are what go test's -run pattern can select:
// runnable tests and fuzz targets, plus examples, and never helpers or
// methods — so an ephemeral pattern is judged against the same set the
// harness would consult.
func TestRunSelectableNamesMatchTheHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture tree")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":    "module example.com/sel\n\ngo 1.26\n",
		"p.go":      "package sel\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package sel\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F() }\n\nfunc FuzzF(f *testing.F) { f.Fuzz(func(t *testing.T, x int) { _ = F() }) }\n\nfunc ExampleF() {\n\t_ = F()\n\t// Output:\n}\n\nfunc helperNotSelectable(t *testing.T) {}\n\nfunc TestingNotATest() {}\n\nfunc Examplefoo() {}\n",
		"x_test.go": "package sel_test\n\nimport \"testing\"\n\nfunc TestExternalVariant(t *testing.T) {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	names, err := tr.RunSelectableNamesContext(context.Background(), "example.com/sel")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ExampleF", "FuzzF", "TestExternalVariant", "TestF"}
	if !slices.Equal(names, want) {
		t.Fatalf("run-selectable names = %v, want %v", names, want)
	}
}
