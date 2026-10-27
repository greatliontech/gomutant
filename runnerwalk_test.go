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
// program is the literal "go", a git command spawned anywhere but
// the git runner's home (internal/gitcmd), a gotool.Runner composed or
// declared anywhere but the runner's own home and the Windows oracle
// arm (which
// prepares the oracle under the plain policy with the tree's hook and
// installs the job object over it), or a gofresh session constructed in
// a file that never sets Runner. A source walk over the real
// tree is a hand-edit oracle; goCommandSpawnsOutsideTheRunner's logic
// is pinned over synthetic sources below.
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
	filepath.Join("internal", "gitcmd", "gitcmd.go"):          true,
	filepath.Join("internal", "engine", "runner.go"):          true,
	filepath.Join("internal", "engine", "process_windows.go"): true,
}

// gitHomes is the one file allowed to spawn git: the git runner's home
// (internal/gitcmd), through which every other git read goes.
var gitHomes = map[string]bool{
	filepath.Join("internal", "gitcmd", "gitcmd.go"): true,
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
					if lit, ok := n.Args[program].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						switch lit.Value {
						case `"go"`:
							report(n.Pos(), "exec."+sel.Sel.Name+`(…, "go", …)`)
						case `"git"`:
							if !gitHomes[path] {
								report(n.Pos(), "exec."+sel.Sel.Name+`(…, "git", …)`)
							}
						}
					}
				}
			case "gofresh":
				if sel.Sel.Name == "NewSession" && !sessionCallHasRunner(file, n) {
					report(n.Pos(), "gofresh.NewSession without Runner")
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
	return out
}

func sessionPolicyLitHasRunner(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "SessionPolicy" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "gofresh" {
		return false
	}
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Runner" {
				return true
			}
		}
	}
	return false
}

func sessionCallHasRunner(file *ast.File, call *ast.CallExpr) bool {
	if len(call.Args) < 3 {
		return false
	}
	switch arg := call.Args[2].(type) {
	case *ast.CompositeLit:
		return sessionPolicyLitHasRunner(arg)
	case *ast.Ident:
		var scope ast.Node = file
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil || call.Pos() < n.Pos() || call.End() > n.End() {
				return false
			}
			switch n.(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				scope = n
			}
			return true
		})
		hasRunner := false
		ast.Inspect(scope, func(n ast.Node) bool {
			if n == nil || n.Pos() >= call.Pos() {
				return false
			}
			switch stmt := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range stmt.Lhs {
					switch l := lhs.(type) {
					case *ast.Ident:
						if l.Name == arg.Name && i < len(stmt.Rhs) {
							if lit, ok := stmt.Rhs[i].(*ast.CompositeLit); ok {
								hasRunner = sessionPolicyLitHasRunner(lit)
							} else {
								hasRunner = false
							}
						}
					case *ast.SelectorExpr:
						if x, ok := l.X.(*ast.Ident); ok && x.Name == arg.Name && l.Sel.Name == "Runner" {
							hasRunner = true
						}
					}
				}
			case *ast.ValueSpec:
				for i, name := range stmt.Names {
					if name.Name == arg.Name {
						if i < len(stmt.Values) {
							if lit, ok := stmt.Values[i].(*ast.CompositeLit); ok {
								hasRunner = sessionPolicyLitHasRunner(lit)
							} else {
								hasRunner = false
							}
						} else {
							hasRunner = false
						}
					}
				}
			}
			return true
		})
		return hasRunner
	default:
		return false
	}
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
	_ = exec.CommandContext(ctx, "git", "status")
	_ = gotool.Runner{}
	var plain gotool.Runner
	_, _ = goRunner.Run(ctx, dir, env, "list")
	_, _ = gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: dir}, gofresh.SessionPolicy{})
}
`)
	got := goCommandSpawnsOutsideTheRunner(filepath.Join("internal", "x", "x.go"), src)
	want := []string{
		`exec.Command(…, "go", …)`, `exec.CommandContext(…, "go", …)`,
		`exec.Command(…, "git", …)`, `exec.CommandContext(…, "git", …)`,
		"gotool.Runner{…}", "var … gotool.Runner",
		"gofresh.NewSession without Runner",
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
	if got := goCommandSpawnsOutsideTheRunner("x.go", []byte("package p\nimport \"github.com/greatliontech/gofresh\"\nfunc f(dir string) { _, _ = gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: dir}, gofresh.SessionPolicy{Runner: r}) }\n")); len(got) != 0 {
		t.Fatalf("a session under Runner reported: %v", got)
	}
	mixed := []byte("package p\nimport \"github.com/greatliontech/gofresh\"\nfunc f(dir string) {\n\t_, _ = gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: dir}, gofresh.SessionPolicy{Runner: r})\n\t_, _ = gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: dir}, gofresh.SessionPolicy{})\n}\n")
	if got := goCommandSpawnsOutsideTheRunner("x.go", mixed); len(got) != 1 || !strings.HasSuffix(got[0], "x.go:5:9: gofresh.NewSession without Runner") {
		t.Fatalf("mixed NewSession calls reported %v, want [x.go:5:9: gofresh.NewSession without Runner]", got)
	}
}
