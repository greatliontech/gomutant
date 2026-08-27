package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
)

// TestEngineReadsUnadjustedPositions pins the coordinate-system rule at
// the package boundary: on-disk identity is the engine's one position
// system (targeting.md's unadjusted-position doctrine), so no
// production file may call the adjusted Position — a //line directive
// must never re-key a target, a drift verdict, a survivor anchor, or a
// file classification. The single sanctioned exception is coverage.go,
// which IS the translation seam: it enumerates directive spellings so
// profile ingest can normalize them away.
func TestEngineReadsUnadjustedPositions(t *testing.T) {
	adjustedCall := func(call *ast.CallExpr) bool {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "Position" {
			return true
		}
		if sel.Sel.Name == "PositionFor" && len(call.Args) == 2 {
			if lit, ok := call.Args[1].(*ast.Ident); ok && lit.Name == "true" {
				return true
			}
		}
		return false
	}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		var inSeam func(pos token.Pos) bool = func(token.Pos) bool { return false }
		if path == "coverage.go" {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "DirectiveCoverage" {
					start, end := fn.Pos(), fn.End()
					inSeam = func(pos token.Pos) bool { return pos >= start && pos < end }
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !adjustedCall(call) || inSeam(call.Pos()) {
				return true
			}
			t.Errorf("%s: adjusted position read at %s — use PositionFor(pos, false); a //line directive must not remap engine positions (DirectiveCoverage is the one translation seam)", path, fset.Position(call.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// writeLineDirectiveModule lays out a module whose target files carry
// //line directives: gen.go remaps itself to a phantom name, and
// decoy.go remaps itself to a *_test.go spelling — the two hazards
// (phantom IO identity, test-file misclassification) in one fixture.
func writeLineDirectiveModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/ld\n\ngo 1.27\n",
		"gen.go": `//line phantom.y:100
package ld

func Sub(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

//line phantom2.y:50
func Sub2(v int) int { return v - 1 }
`,
		"decoy.go": `//line decoy_test.go:1
package ld

func Twice(v int) int { return v * 2 }
`,
		"blockform.go": `package ld

func Block(v int) int { /*line c.y:300*/ return v + 3 }
`,
		"other.go": `package ld

func Other(v int) int { return v + 5 }
`,
		"gen2.go": `//line other.go:200
package ld

func Gen2(v int) int { return v + 7 }
`,
		"xt_test.go": `//line other.go:900
package ld_test

import (
	"testing"

	"example.com/ld"
)

func TestOtherExternal(t *testing.T) {
	if ld.Other(2) != 7 {
		t.Fatal("other")
	}
}
`,
		"prodt.go": `//line ld_test.go:900
package ld

func Prodt(v int) int { return v + 19 }
`,
		"prodx.go": `//line xt_test.go:900
package ld

func Prodx(v int) int { return v + 17 }
`,
		"genA.go": `//line grammar.y:10
package ld

func GenA(v int) int { return v + 11 }
`,
		"genB.go": `//line grammar.y:40
package ld

func GenB(v int) int { return v + 13 }
`,
		"decoy2_test.go": `//line hidden.go:1
package ld

import "testing"

func TestHidden(t *testing.T) {
	if Twice(1) != 2 {
		t.Fatal("twice")
	}
}
`,
		"ld_test.go": `package ld

import "testing"

func TestAll(t *testing.T) {
	if Sub(3, 1) != 2 || Sub(1, 3) != 2 {
		t.Fatal("sub")
	}
	if Twice(2) != 4 {
		t.Fatal("twice")
	}
	if Other(1) != 6 {
		t.Fatal("other")
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLineDirectiveTargetsKeepOnDiskIdentity: a target inside a //line
// file generates candidates from the ON-DISK source under ON-DISK
// coordinates — the adjusted read chain refused it as phantom drift
// (ReadFile of the directive name, ENOENT) and minted phantom
// file:line anchors — and a directive spelling a *_test.go name does
// not reclassify a production file out of the target surface.
func TestLineDirectiveTargetsKeepOnDiskIdentity(t *testing.T) {
	dir := writeLineDirectiveModule(t)
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := tr.CandidatesContext(context.Background(), "example.com/ld.Sub", 0)
	if err != nil {
		t.Fatalf("candidate generation over a //line file: %v", err)
	}
	if len(generation.Candidates) == 0 {
		t.Fatal("no candidates over a //line target")
	}
	for _, c := range generation.Candidates {
		if !strings.HasPrefix(c.Position, "gen.go:") {
			t.Fatalf("candidate position %q not anchored to the on-disk file", c.Position)
		}
		// On-disk lines: the function body spans lines 4-9 of gen.go;
		// the directive would have shifted them past 100. The extent
		// must speak the same coordinates — it is the coverage-join
		// geometry, and the profile's lines are on-disk.
		line, err := anchorLine(c.Position)
		if err != nil {
			t.Fatalf("position %q: %v", c.Position, err)
		}
		if line >= 100 {
			t.Fatalf("candidate position %q carries directive-adjusted lines", c.Position)
		}
		if c.Extent != "" {
			var sl, sc, el, ec int
			if n, err := fmt.Sscanf(c.Extent, "%d:%d-%d:%d", &sl, &sc, &el, &ec); err != nil || n != 4 {
				t.Fatalf("extent %q unparseable: %v", c.Extent, err)
			}
			if sl >= 100 || el >= 100 {
				t.Fatalf("candidate extent %q carries directive-adjusted lines", c.Extent)
			}
		}
	}
	symbols, err := tr.DeclaredSymbolsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(symbols, "example.com/ld.Twice") {
		t.Fatal("a //line *_test.go directive reclassified a production file out of the target surface")
	}
	if !slices.Contains(symbols, "example.com/ld.Sub") {
		t.Fatal("the //line target file is missing from the surface")
	}
	// The inverse misclassification: an on-disk *_test.go whose
	// directive claims a non-test name must STAY a test file — its
	// tests remain listable oracles.
	tests, err := tr.TestsOfContext(context.Background(), "example.com/ld")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tests, "example.com/ld.TestHidden") {
		t.Fatalf("a //line non-test directive reclassified a test file out of the oracle surface: %v", tests)
	}
}

// anchorLine extracts the line from a "file:line:col" survivor anchor.
func anchorLine(position string) (int, error) {
	parts := strings.Split(position, ":")
	if len(parts) < 3 || parts[1] == "" {
		return 0, os.ErrInvalid
	}
	return strconv.Atoi(parts[1])
}

// TestCoverageNormalizesDirectiveNames: cmd/cover keys a //line file's
// blocks under the DIRECTIVE's filename while numbering lines by the
// on-disk file (measured, not assumed); profile ingest re-keys those
// blocks to the on-disk spelling via DirectiveCoverage, so coverage
// joins speak the engine's one coordinate system and a survivor in a
// //line file classifies instead of silently missing.
func TestCoverageNormalizesDirectiveNames(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test with coverage")
	}
	dir := writeLineDirectiveModule(t)
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	view := tr.DirectiveCoverage()
	renames := view.Renames
	if got := renames["example.com/ld/phantom.y"]; got != "example.com/ld/gen.go" {
		t.Fatalf("rename map %v missing the phantom mapping", renames)
	}
	// One file may claim several directive names — each maps back
	// independently (a generated file with per-region directives).
	if got := renames["example.com/ld/phantom2.y"]; got != "example.com/ld/gen.go" {
		t.Fatalf("rename map %v missing the second directive mapping of the same file", renames)
	}
	// A directive naming an EXTERNAL test file registers: cover
	// instruments external variants under their own import path, so
	// the name binds nothing in the base namespace — treating it as a
	// sibling would refuse a correct rename and manufacture
	// never-executed for a covered file.
	if got := renames["example.com/ld/xt_test.go"]; got != "example.com/ld/prodx.go" {
		t.Fatalf("rename map %v refused the external-test-named directive", renames)
	}
	// The same rule one predicate further: an IN-PACKAGE test file is
	// never instrumented either, so a directive naming one binds a
	// key nothing else claims — the rename registers and the carrier
	// stays sound.
	if got := renames["example.com/ld/ld_test.go"]; got != "example.com/ld/prodt.go" {
		t.Fatalf("rename map %v refused the in-package-test-named directive", renames)
	}
	// Block-form directive ahead of same-line code: cover keys that
	// code under c.y, and the node walk must see it (a per-line walk
	// anchored on line starts missed it).
	if got := renames["example.com/ld/c.y"]; got != "example.com/ld/blockform.go" {
		t.Fatalf("rename map %v missing the block-form mapping", renames)
	}
	// Collision guard: gen2.go's directive names the real sibling
	// other.go — cover merges both files under other.go's key, so a
	// rename would steal the real file's coverage. No rename: the
	// directive file's bucket stays honestly empty. The same key is
	// also claimed by xt_test.go (an EXTERNAL test package's
	// directive): per-variant guard sets saw no production siblings
	// and waved that theft through — the union-scoped sets refuse it.
	if _, stolen := renames["example.com/ld/other.go"]; stolen {
		t.Fatalf("rename map %v stole a real sibling's coverage key", renames)
	}
	// Contention: genA.go and genB.go both open //line grammar.y —
	// the generated-from-one-grammar shape. cover merges both files'
	// blocks under one key; handing the merge to either steals from
	// the other, so contention registers nothing.
	if _, contended := renames["example.com/ld/grammar.y"]; contended {
		t.Fatalf("rename map %v handed a contended directive key to one claimant", renames)
	}
	// A test file never appears as a rename's on-disk side: coverage
	// consumers only query target and replacement files, so the entry
	// would be pure key-theft risk.
	for key, value := range renames {
		if strings.HasSuffix(value, "_test.go") {
			t.Fatalf("rename %q -> %q carries a test file as its on-disk side", key, value)
		}
	}
	// Every refusal is REPRESENTABLE: contention claimants and both
	// collision sides are marked unsound, so downstream buckets stay
	// empty instead of manufacturing never-executed.
	for _, f := range []string{"example.com/ld/genA.go", "example.com/ld/genB.go", "example.com/ld/gen2.go", "example.com/ld/other.go"} {
		if !view.Unsound[f] {
			t.Fatalf("unsound set %v missing %s: a refused re-keying must be representable downstream", view.Unsound, f)
		}
	}
	for _, f := range []string{"example.com/ld/gen.go", "example.com/ld/blockform.go", "example.com/ld/prodx.go", "example.com/ld/prodt.go"} {
		if view.Unsound[f] {
			t.Fatalf("unsound set %v wrongly marks %s", view.Unsound, f)
		}
	}
	coverage, err := CoveredPositions(context.Background(), dir, "example.com/ld", "TestAll", "example.com/ld", time.Minute, nil, tr.GoEnv(), view)
	if err != nil {
		t.Fatalf("coverage probe: %v", err)
	}
	if !coverage.CoversFile("example.com/ld/gen.go") {
		t.Fatal("coverage not re-keyed to the on-disk file: a //line survivor would silently miss its bucket")
	}
	if coverage.CoversFile("example.com/ld/phantom.y") {
		t.Fatal("directive-named coverage key survived ingest")
	}
	// The collision victim's raw entry survives ingest (cover merged
	// both files there), but the VERDICT layer refuses it: polluted
	// coverage is not evidence.
	if !coverage.CoversFile("example.com/ld/other.go") {
		t.Fatal("the real sibling's raw profile entry vanished at ingest")
	}
	if !coverage.Unsound("example.com/ld/other.go") {
		t.Fatal("the collision victim's coverage was not marked unsound: a polluted entry would serve as a verdict")
	}
}

// TestDirectiveCoverageRequiresCompiledOnDiskSource pins the value-side
// guard in isolation: syntax whose on-disk file is NOT one of the
// package's compiled sources (the cgo-intermediate shape — a build
// cache blob) registers NO rename even when its directive
// back-reference names something no compiled sibling masks. Without
// the guard, the blob-valued junk entry would ride the map and the
// spec's "a cgo intermediate registers nothing" would be false for
// back-references outside the compiled set.
func TestDirectiveCoverageRequiresCompiledOnDiskSource(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "/cache/blob.go", "//line other.y:5\npackage p\n\nfunc F() int { return 1 }\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &packages.Package{PkgPath: "example.com/p", GoFiles: []string{"/real/real.go"}, Fset: fset, Syntax: []*ast.File{f}}
	tr := &Tree{pkgs: []*packages.Package{pkg}}
	if view := tr.DirectiveCoverage(); len(view.Renames) != 0 || len(view.Unsound) != 0 {
		t.Fatalf("blob-backed syntax registered %v / %v: the on-disk side must be one of the package's compiled sources", view.Renames, view.Unsound)
	}
}

// TestDirectiveCoverageSkipsCgoIntermediates: a cgo package's loaded
// syntax is the cgo-processed cache blobs, whose //line
// back-references point AT the real sources — a rename registered
// from them would run BACKWARD (real file -> blob name) and steal the
// real file's coverage key, flipping covered replacements to a false
// "unexercised". The on-disk-side guard (the file must be one of the
// package's own compiled sources) drops them wholesale.
func TestDirectiveCoverageSkipsCgoIntermediates(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a cgo package")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/cg\n\ngo 1.27\n",
		"c.go": `package cg

// #include <stdlib.h>
import "C"

func CAdd(a, b int) int { return a + b + int(C.atoi(C.CString("0"))) }
`,
		"cg_test.go": `package cg

import "testing"

func TestAll(t *testing.T) {
	if CAdd(1, 2) != 3 {
		t.Fatal("cadd")
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Skipf("cgo unavailable: %v", err)
	}
	view := tr.DirectiveCoverage()
	for key, value := range view.Renames {
		t.Errorf("cgo package registered rename %q -> %q: the blob back-reference would steal the real source's coverage key", key, value)
	}
	for f := range view.Unsound {
		t.Errorf("cgo package marked %q unsound: blob refusals are not coverage refusals", f)
	}
}
