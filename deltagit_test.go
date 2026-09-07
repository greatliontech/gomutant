package gomutant_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/gitref"
)

// End to end over a git-backed fixture: lines inserted into Weak's body
// since HEAD are the delta, and the survivors a measurement leaves on
// them are the on-delta cut while the body's pre-existing survivors are
// the remainder; a record measured before the edit cannot be placed
// (REQ-exec-run-status, REQ-target-changed).
func TestDeltaCutOverAMeasuredChange(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(filepath.Join("internal", "engine", "testdata", "fixturemod"))); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	ctx := context.Background()
	target := gomutant.Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	before, err := gomutant.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := before.Run(ctx, []gomutant.Target{target}, gomutant.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, "lib", "lib.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Two statements inserted at the top of Weak's body: the delta.
	edited := strings.Replace(string(src), "func Weak(x int) int {\n", "func Weak(x int) int {\n\tif x < -100 {\n\t\treturn x + 1\n\t}\n", 1)
	if edited == string(src) {
		t.Fatal("Weak anchor missing")
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	surface, err := gitref.ChangedSurfaceContext(ctx, tmp, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if ranges := surface.Added["lib/lib.go"]; len(ranges) != 1 || ranges[0].From != 39 || ranges[0].To != 42 {
		t.Fatalf("added lines = %v, want [39,42) in lib/lib.go", surface.Added)
	}
	tr, err := gomutant.Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	targets, _, cut, err := tr.DiscoverChangedSurfaceContext(ctx, surface, func(p string) ([]byte, bool) {
		return gitref.ShowContext(ctx, tmp, "HEAD", p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Symbol != target.Symbol || !cut.Changed[target.Symbol] {
		t.Fatalf("changed discovery = %+v, cut %+v; want Weak alone", targets, cut.Changed)
	}
	// The pre-edit record's positions cannot be placed against the
	// edited file: everything is remainder.
	if split, err := tr.CutSurvivorsContext(ctx, stale[0], cut); err != nil || len(split.OnDelta) != 0 {
		t.Fatalf("stale record placed on the delta: %+v, %v", split.OnDelta, err)
	}
	findings, err := tr.Run(ctx, []gomutant.Target{target}, gomutant.Options{})
	if err != nil {
		t.Fatal(err)
	}
	split, err := tr.CutSurvivorsContext(ctx, findings[0], cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(split.OnDelta) == 0 || len(split.Remainder) == 0 {
		t.Fatalf("cut = %d on delta, %d remainder; want survivors on both sides of the edit (open: %+v)", len(split.OnDelta), len(split.Remainder), findings[0].Open())
	}
	line := func(position string) string { return strings.Split(position, ":")[1] }
	for _, s := range split.OnDelta {
		if l := line(s.Position); l != "39" && l != "40" && l != "41" {
			t.Fatalf("on-delta survivor %s lies outside the added lines", s.Position)
		}
	}
	for _, s := range split.Remainder {
		if l := line(s.Position); l == "39" || l == "40" || l == "41" {
			t.Fatalf("remainder survivor %s lies on the added lines", s.Position)
		}
	}
}
