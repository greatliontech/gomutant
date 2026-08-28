package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
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
func TestRecvTypeNameUnwrapsParenthesizedForms(t *testing.T) {
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
}
