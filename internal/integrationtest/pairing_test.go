package integrationtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every package carrying a -short gate takes the selection's default:
// its test files declare a TestMain that calls DefaultToShort, directly
// or through testsuite.Main. Pinned over the real tree (a hand-edit
// oracle — a probe through the build overlay never reaches a file the
// walk reads from disk) and, below, over synthetic sources.
func TestEveryGatedPackageTakesTheSelectionsDefault(t *testing.T) {
	root := filepath.Join("..", "..")
	gated, problems, err := unpairedPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	// The walk's reach is itself pinned, as gofresh's shortgates pins
	// its own: a misrooted or empty walk finds no gated package and
	// would pass over the pairings it never read.
	if len(gated) == 0 {
		t.Fatalf("the walk under %s found no package carrying a gate; the partition is gone or the walk is misrooted", root)
	}
	if !gated[root] {
		t.Fatalf("the walk found no gate in %s itself (the root package carries them)", root)
	}
	if len(problems) > 0 {
		t.Fatalf("packages carrying -short gates without a TestMain taking the selection's default:\n  %s", strings.Join(problems, "\n  "))
	}
}

// The walk's own arms: a gated package with the call passes, one whose
// TestMain lacks the call is named, one without a TestMain is named, an
// ungated package needs nothing.
func TestUnpairedPackagesNamesTheGatedWithoutTheDefault(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The gate text is composed so gofresh's shortgates string arm,
	// which guards the mechanical pass, does not read this fixture as
	// a planted gate.
	gate := "package p\n\nimport \"testing\"\n\nfunc TestHeavy(t *testing.T) {\n\tif testing." + "Short() {\n\t\tt.Skip(\"x\")\n\t}\n}\n"
	write("paired/a_test.go", gate)
	write("paired/main_test.go", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/x/internal/integrationtest\"\n)\n\nfunc TestMain(m *testing.M) {\n\tintegrationtest.DefaultToShort()\n\tos.Exit(m.Run())\n}\n")
	write("viasuite/a_test.go", gate)
	write("viasuite/main_test.go", "package p\n\nimport (\n\t\"testing\"\n\n\t\"example.com/x/internal/testsuite\"\n)\n\nfunc TestMain(m *testing.M) {\n\ttestsuite.Main(m, \"testdata\")\n}\n")
	write("lacking/a_test.go", gate)
	write("lacking/main_test.go", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) {\n\tos.Exit(m.Run())\n}\n")
	write("nomain/a_test.go", gate)
	write("ungated/a_test.go", "package p\n\nimport \"testing\"\n\nfunc TestLight(t *testing.T) {}\n")
	write("testdata/skipped/a_test.go", gate)
	gated, got, err := unpairedPackages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lacking: TestMain takes no selection default", "nomain: no TestMain"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("unpaired = %q, want %q", got, want)
	}
	if len(gated) != 4 || gated[filepath.Join(root, "testdata", "skipped")] || gated[filepath.Join(root, "ungated")] {
		t.Fatalf("gated = %v, want the four gate-carrying packages, testdata skipped, the ungated one absent", gated)
	}
	// A compound or negated condition consults the flag too: the walk
	// reads the call, not the if's shape.
	write("negated/a_test.go", "package p\n\nimport \"testing\"\n\nfunc TestHeavy(t *testing.T) {\n\tif !testing."+"Short() {\n\t\tt.Log(\"x\")\n\t}\n}\n")
	if gated, _, err := unpairedPackages(root); err != nil || !gated[filepath.Join(root, "negated")] {
		t.Fatalf("a negated gate not read as consulting the flag: %v, %v", gated, err)
	}
}

// unpairedPackages walks root's packages (testdata and dot directories
// excluded), returns the set of package directories whose test files
// consult testing.Short() — any call, whatever the if's shape, so the
// walk agrees with gofresh's shortgates on what consults the flag — and
// names each such package whose TestMain does not call DefaultToShort
// or testsuite.Main.
func unpairedPackages(root string) (map[string]bool, []string, error) {
	gated := map[string]bool{}
	paired := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Short" && len(n.Args) == 0 {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "testing" {
						gated[dir] = true
					}
				}
			case *ast.FuncDecl:
				if n.Name.Name != "TestMain" || n.Recv != nil || n.Body == nil {
					return true
				}
				verdict := "TestMain takes no selection default"
				ast.Inspect(n.Body, func(c ast.Node) bool {
					call, ok := c.(*ast.CallExpr)
					if !ok {
						return true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && ((pkg.Name == "integrationtest" && sel.Sel.Name == "DefaultToShort") || (pkg.Name == "testsuite" && sel.Sel.Name == "Main")) {
							verdict = ""
						}
					}
					return true
				})
				paired[dir] = verdict
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	var problems []string
	for dir := range gated {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return nil, nil, err
		}
		verdict, ok := paired[dir]
		switch {
		case !ok:
			problems = append(problems, rel+": no TestMain")
		case verdict != "":
			problems = append(problems, rel+": "+verdict)
		}
	}
	sort.Strings(problems)
	return gated, problems, nil
}
