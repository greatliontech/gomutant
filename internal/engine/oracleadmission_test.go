package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestAliasOracleMatchesGoHarness compares discovery with the actual harness,
// including a type alias declared in another test file and the only assertion
// capable of detecting the mutation. The fuzz seed remains in the inventory.
func TestAliasOracleMatchesGoHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and executes the alias fixture")
	}
	dir := t.TempDir()
	const pkg = "example.com/aliasoracle"
	files := map[string]string{
		"go.mod":        "module " + pkg + "\n\ngo 1.27.0\n",
		"p.go":          "package p\nfunc Value(n int) int { return n + 1 }\n",
		"types_test.go": "package p\nimport \"testing\"\ntype T = testing.T\ntype F = testing.F\n",
		"p_test.go": `package p
import "testing"
func TestWeak(t *testing.T) { _ = Value(2) }
func TestAlias(t *T) { if Value(2) != 3 { t.Fatal("wrong value") } }
func FuzzAlias(f *F) {
 f.Add(1)
 f.Fuzz(func(t *T, n int) { _ = Value(n) })
}
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	// This is Go's own inventory, independently of both Gomutant walks.
	out, err := goRunner.Run(ctx, dir, GoEnv(dir), "test", "-list=.", ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestWeak", "TestAlias", "FuzzAlias"} {
		if !slices.Contains(strings.Split(string(out), "\n"), name) {
			t.Fatalf("Go did not enumerate %s: %s", name, out)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{pkg + ".FuzzAlias", pkg + ".TestAlias", pkg + ".TestWeak"}
	got := tr.TestsOf(pkg)
	if !slices.Equal(got, want) {
		t.Errorf("derived oracle = %v, want %v", got, want)
	}
	if err := tr.ValidateOracleContext(ctx, want); err != nil {
		t.Errorf("explicit alias oracle refused: %v", err)
	}
	if err := tr.VerifyTestEnumerationContext(ctx, pkg, want); err != nil {
		t.Errorf("disk inventory differs from Go: %v", err)
	}
	var names []string
	for _, symbol := range got {
		names = append(names, strings.TrimPrefix(symbol, pkg+"."))
	}
	pattern := "^(" + strings.Join(names, "|") + ")$"
	mutants, err := tr.Mutants(pkg+".Value", 0)
	if err != nil || len(mutants) == 0 {
		t.Fatalf("mutants: %v, %v", mutants, err)
	}
	for _, mutant := range mutants {
		if mutant.Operator != "arithmetic: + -> -" {
			continue
		}
		outcome, killer, _, err := RunMutant(ctx, dir, mutant, []string{pkg}, pattern, time.Minute, nil, OracleBounds{})
		if err != nil || outcome != MutantKilled || killer != pkg+".TestAlias" {
			t.Fatalf("alias assertion: outcome=%v killer=%q err=%v", outcome, killer, err)
		}
		return
	}
	t.Fatal("arithmetic mutation not found")
}

// Each deciding terminal event belongs to its named top-level test, not to
// its parent process's exit or a sibling/subtest's terminal event.
func TestBaselinePassesAreNamedNotCounted(t *testing.T) {
	stream, err := parseTestStream([]byte(`
{"Action":"pass","Package":"p","Test":"TestA/child"}
{"Action":"skip","Package":"p","Test":"TestA"}
{"Action":"fail","Package":"p","Test":"TestB"}
{"Action":"pass","Package":"p","Test":"TestC"}
{"Action":"run","Package":"p","Test":"TestD"}
{"Action":"pass","Package":"p"}
`))
	if err != nil || stream.ran != 3 || !slices.Equal(stream.passed, []string{"p.TestC"}) ||
		!slices.Equal(stream.failed, []string{"TestB"}) || !slices.Equal(stream.truncated, []string{"TestD"}) {
		t.Fatalf("named terminals = %+v, %v", stream, err)
	}
	if _, err := parseTestStream([]byte(`{"Action":"pass","Package":"p","Test":"TestC"} torn`)); err == nil {
		t.Fatal("a valid prefix concealed a corrupt stream tail")
	}
}

func TestBaselineRejectsEqualCountDifferentPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("executes both baseline observations")
	}
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod": "module example.com/passswap\n\ngo 1.27.0\n",
		"p.go":   "package p\n",
		"phase":  "0",
		"p_test.go": `package p
import ("os"; "testing")
var initial []byte
func TestA(t *testing.T) {
 var err error
 initial, err = os.ReadFile("phase")
 if err != nil { t.Fatal(err) }
 if string(initial) != "0" { t.Skip("second run") }
 if err := os.WriteFile("phase", []byte("1"), 0600); err != nil { t.Fatal(err) }
}
func TestB(t *testing.T) {
 if string(initial) == "0" { t.Skip("first run") }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := TestBaselineObservedEnv(context.Background(), dir, "example.com/passswap", "^Test(A|B)$", time.Minute, nil, dir, dir, nil, nil, GoEnv(dir), OracleBounds{})
	if err == nil || !strings.Contains(err.Error(), "baseline passing test identities changed") {
		t.Fatalf("equal-count changed identities admitted: %v", err)
	}
}
