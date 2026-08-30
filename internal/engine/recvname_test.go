package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one receiver reducer — recvTypeName, shared by the discovery
// grammar and the structural prober — reduces every legal receiver
// form, the parenthesized ones included: a method whose receiver
// wears parentheses is nameable exactly like its bare twin. The
// convention shared across the tools is AST-side DECLARATION
// naming — the declarer's name, every reducer unwrapping this same
// grammar; method-set-based resolution elsewhere (stipulator's
// lookup, gofresh's types-side subject walk) is deliberately wider
// (promotion), which never concerns this reducer: a promoted
// method has no declaration under the embedder to mutate.
func TestRecvTypeNameReducesEveryReceiverForm(t *testing.T) {
	cases := []struct {
		recv string
		want string
	}{
		{"T", "T"},
		{"*T", "T"},
		{"(*T)", "T"},
		{"((*T))", "T"},
		{"(((T)))", "T"},
		{"(T)", "T"},
		{"(P[X])", "P"},
		{"((*P[X, Y]))", "P"},
		{"pkg.T", ""}, // a selector receiver is a type error the grammar refuses
		{"[]T", ""},   // as is any non-name form
	}
	for _, tc := range cases {
		src := "package p\ntype T struct{}\nfunc (r " + tc.recv + ") M() {}\n"
		f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.recv, err)
		}
		var fd *ast.FuncDecl
		for _, d := range f.Decls {
			if x, ok := d.(*ast.FuncDecl); ok && x.Recv != nil {
				fd = x
			}
		}
		if got := recvTypeName(fd); got != tc.want {
			t.Errorf("recvTypeName(recv %s) = %q, want %q", tc.recv, got, tc.want)
		}
	}
	// The entry guard's second operand is reachable only from a
	// hand-constructed AST — the parser never yields a receiver-bearing
	// declaration with an empty field list — so construct it directly:
	// the pinned outcome is the guard's "" refusal, not an index panic.
	if got := recvTypeName(&ast.FuncDecl{Name: ast.NewIdent("M"), Recv: &ast.FieldList{}}); got != "" {
		t.Errorf(`recvTypeName(empty receiver list) = %q, want ""`, got)
	}
}

// The rewrite's offset guard refuses to splice when the declaring
// file's bytes no longer carry the loaded parse's name span. The
// branch is unreachable in production by construction — every walked
// file is digest-pinned at load and the drift arm fires first — so
// this pins the deliberate defense-in-depth backstop, with the digest
// entry deleted to isolate it: a truncated or same-length-edited file
// yields a silent not-found (the caller's declares-no-method refusal),
// never a corrupt rewrite or a slice panic (REQ-target-structural).
func TestMethodDeclarationRewriteRefusesShiftedOffsets(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/off\n\ngo 1.26\n",
		"p/p.go": "package p\n\ntype Impl struct{}\n\nfunc (Impl) Do() int { return 1 }\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	file, src, ok, err := tr.methodDeclarationRewrite("example.com/off/p.Impl", "Impl", "Do")
	if err != nil || !ok || !strings.Contains(string(src), "Do_gomutantStructuralProbe") {
		t.Fatalf("positive control failed: ok=%v err=%v src=%q", ok, err, src)
	}
	delete(tr.sourceDigests, file)
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// Same-length edit: the name span holds different bytes.
	renamed := strings.Replace(string(original), "func (Impl) Do()", "func (Impl) Dq()", 1)
	if len(renamed) != len(original) || renamed == string(original) {
		t.Fatal("fixture edit did not hold length")
	}
	if err := os.WriteFile(file, []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := tr.methodDeclarationRewrite("example.com/off/p.Impl", "Impl", "Do"); ok || err != nil {
		t.Fatalf("same-length drifted name span = ok %v err %v, want the silent not-found", ok, err)
	}
	// Truncation: the recorded offsets exceed the bytes on disk.
	if err := os.WriteFile(file, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := tr.methodDeclarationRewrite("example.com/off/p.Impl", "Impl", "Do"); ok || err != nil {
		t.Fatalf("truncated declaring file = ok %v err %v, want the silent not-found", ok, err)
	}
}
