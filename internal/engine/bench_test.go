package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/greatliontech/go-x-tools/go/packages"
)

//gofresh:pure
func BenchmarkDeclaredSymbols(b *testing.B) {
	const src = `package sample

type Widget struct{ v int }

func init() {}
func Alpha(x int) int { return x + 1 }
func Beta(a, b string) string { return a + b }
func (w Widget) Value() int { return w.v }
func (w *Widget) Add(d int) { w.v += d }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		b.Fatal(err)
	}
	tree := &Tree{
		pkgs: []*packages.Package{
			{
				PkgPath: "example.com/sample",
				Fset:    fset,
				Syntax:  []*ast.File{file},
			},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		syms := tree.DeclaredSymbols()
		if len(syms) != 5 {
			b.Fatalf("DeclaredSymbols = %d, want 5", len(syms))
		}
	}
}
