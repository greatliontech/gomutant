package gomutant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryGoCommandRidesTheRunner walks the module's production
// sources for a go command spawned outside the tree's runner
// (REQ-exec-go-command-runner): an exec.Command/CommandContext whose
// program is the literal "go", a gotool plain-spawn call (Run,
// SampleGoVersion, TakeEnvSnapshot, Command), a gotool.Runner composed
// or declared anywhere but the runner's own home and the Windows oracle
// arm (which prepares the oracle under the plain policy with the tree's
// hook and installs the job object over it), or a gofresh engine
// constructed in a file that never names WithGoRunner. A source walk
// over the real tree is a
// hand-edit oracle; goCommandSpawnsOutsideTheRunner's logic is pinned
// over synthetic sources below.
func TestEveryGoCommandRidesTheRunner(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != "." && (name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, site := range goCommandSpawnsOutsideTheRunner(path, src) {
			offenders = append(offenders, site)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("go commands spawned outside the tree's runner:\n%s", strings.Join(offenders, "\n"))
	}
}

// runnerHomes are the files allowed to compose a gotool.Runner: the
// runner's declaration and the Windows oracle arm.
var runnerHomes = map[string]bool{
	filepath.Join("internal", "engine", "runner.go"):          true,
	filepath.Join("internal", "engine", "process_windows.go"): true,
}

// goCommandSpawnsOutsideTheRunner reports each call in src that spawns
// a go command without the tree's runner, as "path:line: <spelling>".
func goCommandSpawnsOutsideTheRunner(path string, src []byte) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return []string{path + ": " + err.Error()}
	}
	var out []string
	report := func(pos token.Pos, spelling string) {
		out = append(out, fset.Position(pos).String()+": "+spelling)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch pkg.Name {
			case "exec":
				program := 0
				if sel.Sel.Name == "CommandContext" {
					program = 1
				} else if sel.Sel.Name != "Command" {
					return true
				}
				if len(n.Args) > program {
					if lit, ok := n.Args[program].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"go"` {
						report(n.Pos(), "exec."+sel.Sel.Name+`(…, "go", …)`)
					}
				}
			case "gotool":
				switch sel.Sel.Name {
				case "Run", "SampleGoVersion", "TakeEnvSnapshot", "Command":
					report(n.Pos(), "gotool."+sel.Sel.Name)
				}
			}
		case *ast.CompositeLit:
			if isGotoolRunner(n.Type) && !runnerHomes[path] {
				report(n.Pos(), "gotool.Runner{…}")
			}
		case *ast.ValueSpec:
			if isGotoolRunner(n.Type) && !runnerHomes[path] {
				report(n.Pos(), "var … gotool.Runner")
			}
		}
		return true
	})
	if !strings.Contains(string(src), "WithGoRunner") {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "gofresh" && sel.Sel.Name == "New" {
					report(call.Pos(), "gofresh.New without WithGoRunner")
				}
			}
			return true
		})
	}
	return out
}

func isGotoolRunner(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "gotool" && sel.Sel.Name == "Runner"
}

func TestGoCommandSpawnWalkReadsEverySpelling(t *testing.T) {
	src := []byte(`package p

import (
	"context"
	"os/exec"

	"github.com/greatliontech/gofresh/gotool"
)

func f(ctx context.Context, dir string, env []string) {
	_ = exec.Command("go", "version")
	_ = exec.CommandContext(ctx, "go", "env")
	_ = exec.Command("git", "status")
	_, _ = gotool.Run(ctx, dir, env, "list")
	_, _ = gotool.SampleGoVersion(ctx, dir, env)
	_, _ = gotool.TakeEnvSnapshot(ctx, dir, env)
	_ = gotool.Runner{}
	var plain gotool.Runner
	_, _ = goRunner.Run(ctx, dir, env, "list")
	_, _ = gofresh.New(gofresh.WithDir(dir))
}
`)
	got := goCommandSpawnsOutsideTheRunner(filepath.Join("internal", "x", "x.go"), src)
	want := []string{
		`exec.Command(…, "go", …)`, `exec.CommandContext(…, "go", …)`,
		"gotool.Run", "gotool.SampleGoVersion", "gotool.TakeEnvSnapshot", "gotool.Runner{…}", "var … gotool.Runner",
		"gofresh.New without WithGoRunner",
	}
	if len(got) != len(want) {
		t.Fatalf("walk = %v, want %d sites", got, len(want))
	}
	for i, site := range got {
		if !strings.HasSuffix(site, ": "+want[i]) {
			t.Fatalf("site %d = %q, want %q", i, site, want[i])
		}
	}
	if got := goCommandSpawnsOutsideTheRunner(filepath.Join("internal", "engine", "runner.go"), []byte("package engine\nimport \"github.com/greatliontech/gofresh/gotool\"\nvar r = gotool.Runner{}\n")); len(got) != 0 {
		t.Fatalf("the runner's home reported: %v", got)
	}
	if got := goCommandSpawnsOutsideTheRunner("x.go", []byte("package p\nimport \"github.com/greatliontech/gofresh\"\nfunc f(dir string) { _, _ = gofresh.New(gofresh.WithDir(dir), gofresh.WithGoRunner(r)) }\n")); len(got) != 0 {
		t.Fatalf("an engine under WithGoRunner reported: %v", got)
	}
}
