package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := Load("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestValidateOracleRejectsAmbiguousTestVariants(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/ambiguous\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package ambiguous\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal_test.go"), []byte("package ambiguous\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "external_test.go"), []byte("package ambiguous_test\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.ValidateOracle([]string{"example.com/ambiguous.TestSame"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ValidateOracle = %v, want ambiguity", err)
	}
}

func TestPackageContextSupportsTestOnlyPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testonly\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "only_test.go"), []byte("package testonly_test\n\nimport \"testing\"\nfunc TestOnly(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	moduleDir, packageDir, err := tree.PackageContext("example.com/testonly")
	if err != nil {
		t.Fatal(err)
	}
	if moduleDir != dir || packageDir != dir {
		t.Fatalf("context = %s / %s, want %s", moduleDir, packageDir, dir)
	}
	if err := tree.ValidateOracle([]string{"example.com/testonly.TestOnly"}); err != nil {
		t.Fatal(err)
	}
}

// TestBodyHash pins the body-hash contract (REQ-result-record): stable across
// resolutions, distinct across distinct bodies, 64 hex characters, and
// insensitive to formatting because it hashes canonical text.
func TestBodyHash(t *testing.T) {
	tr := fixtureTree(t)
	h1, err := tr.BodyHash("example.com/fixture/lib.Add")
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 64 || strings.ToLower(h1) != h1 {
		t.Fatalf("body hash %q", h1)
	}
	h2, err := tr.BodyHash("example.com/fixture/lib.Add")
	if err != nil || h1 != h2 {
		t.Fatalf("unstable: %v %s %s", err, h1, h2)
	}
	hw, err := tr.BodyHash("example.com/fixture/lib.Weak")
	if err != nil || hw == h1 {
		t.Fatalf("distinct bodies share a hash: %v", err)
	}
	// Canonically identical bodies spelled with different formatting hash
	// identically — the projection, pinned at the BodyHash level.
	fa, err := tr.BodyHash("example.com/fixture/bodyless.FmtA")
	if err != nil {
		t.Fatal(err)
	}
	fb, err := tr.BodyHash("example.com/fixture/bodyless.FmtB")
	if err != nil || fa != fb {
		t.Fatalf("formatting moved the body hash: %v %s %s", err, fa, fb)
	}
	// A bodyless declaration (assembly-implemented) hashes its whole
	// declaration — and so never collides with a bodied function's body.
	he, err := tr.BodyHash("example.com/fixture/bodyless.Ext")
	if err != nil {
		t.Fatal(err)
	}
	if len(he) != 64 || he == fa {
		t.Fatalf("bodyless declaration hash %q", he)
	}
	if hash := canonHash("func Ext(x int) int"); he != hash {
		t.Fatalf("bodyless hash covers %q-projection, got %s want %s", "whole declaration", he, hash)
	}
}

// TestCanonText pins the canonical projection the hash is computed over:
// whitespace runs collapse, edges trim, and the projection is idempotent —
// formatting churn can never move a body hash.
func TestCanonText(t *testing.T) {
	in := "  if x >\n\t\t0 {  return 1 }  "
	want := "if x > 0 { return 1 }"
	if got := canonText(in); got != want {
		t.Fatalf("canonText = %q, want %q", got, want)
	}
	if canonText(canonText(in)) != canonText(in) {
		t.Fatal("projection not idempotent")
	}
	if canonHash("a  b") != canonHash("a\nb") {
		t.Fatal("formatting moved the hash")
	}

	// Literal interiors are content, not formatting: whitespace inside
	// string, rune, and raw literals is preserved byte-exact, while a
	// quote inside a comment opens no literal (REQ-target-changed's
	// formatting-vs-content line).
	if canonHash(`return "a  b"`) == canonHash(`return "a b"`) {
		t.Fatal("literal-interior whitespace collapsed: different programs share a hash")
	}
	if canonHash("return\t\"a  b\"") != canonHash(`return "a  b"`) {
		t.Fatal("formatting outside the literal moved the hash")
	}
	raw := "x := `raw\n\ttext`  +  2"
	if got := canonText(raw); got != "x := `raw\n\ttext` + 2" {
		t.Fatalf("raw-literal preservation = %q", got)
	}
	escaped := `f("esc\"  q")`
	if canonText(escaped) != escaped {
		t.Fatalf("escaped-quote literal = %q", canonText(escaped))
	}
	commented := "f() // say \"hi   there\"\ng()"
	if canonText(commented) != `f() // say "hi there" g()` {
		t.Fatalf("comment text = %q, want prose collapsed with no literal opened", canonText(commented))
	}
	// The projection is applied once per source, never re-projected: a
	// line comment followed by a literal demonstrates re-projection is
	// lossy (the comment absorbs the literal), which no consumer relies
	// on - both sides of every comparison are single projections.
	oneShot := "x() // c\ny := \"a  b\""
	if canonText(canonText(oneShot)) == canonText(oneShot) {
		t.Fatal("re-projection unexpectedly stable; the single-projection contract comment is stale")
	}
}

// TestResolveSymbols pins the symbol grammar: functions, value- and
// pointer-receiver methods, generic receivers with parameters stripped, and
// the failure modes — a missing identifier, a missing package, a non-function
// symbol.
func TestResolveSymbols(t *testing.T) {
	tr := fixtureTree(t)
	for _, sym := range []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/methods.Counter.Inc",   // pointer receiver
		"example.com/fixture/methods.Counter.Value", // value receiver
		"example.com/fixture/methods.Box.Get",       // generic receiver, params stripped
		"example.com/fixture/lib.TestAdd",           // test function (oracle symbol)
		"example.com/fixture/dot.x.F",               // dotted import path: longest package match wins
		"example.com/fixture/lib.TestExt",           // oracle in an external test package ("lib_test")
	} {
		if _, _, err := tr.funcDecl(sym); err != nil {
			t.Errorf("funcDecl(%s): %v", sym, err)
		}
	}
	if _, _, err := tr.funcDecl("example.com/fixture/lib.NoSuch"); err == nil {
		t.Error("missing identifier resolved")
	}
	if _, _, err := tr.funcDecl("example.com/nosuch.Thing"); err == nil {
		t.Error("missing package resolved")
	}
	// A symbol in a package with load errors surfaces the load error rather
	// than reading as merely missing.
	if _, _, err := tr.funcDecl("example.com/fixture/broken.F"); err == nil || !strings.Contains(err.Error(), "load errors") {
		t.Errorf("broken package: err = %v, want load-errors surfaced", err)
	}
	// A resolvable non-function has no body to hash or mutate.
	if _, err := tr.BodyHash("example.com/fixture/lib.I"); !errors.Is(err, ErrNotFunction) {
		t.Errorf("type symbol: err = %v, want ErrNotFunction", err)
	}
	// An interface method resolves through the value method set (the pointer
	// set is empty for interfaces) but declares no body — nothing to mutate.
	if _, err := tr.BodyHash("example.com/fixture/lib.I.M"); !errors.Is(err, ErrNotFunction) {
		t.Errorf("interface method: err = %v, want ErrNotFunction", err)
	}
}

// TestLoadWorkspace pins workspace loading: symbols of every go.work member
// resolve, and a member escaping the tree is refused — hermeticity, never
// bent.
func TestLoadWorkspace(t *testing.T) {
	tr, err := Load("testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.funcDecl("example.com/ws.Root"); err != nil {
		t.Fatalf("root member symbol: %v", err)
	}
	if _, _, err := tr.funcDecl("example.com/ws/sub.Nested"); err != nil {
		t.Fatalf("nested member symbol: %v", err)
	}
	if _, err := Load("testdata/escapemod"); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
		t.Fatalf("escaping go.work member accepted: %v", err)
	}
}
