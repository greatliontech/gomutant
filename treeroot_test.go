package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// TestLoadCanonicalizesTheTreeRoot pins the load boundary's one
// coordinate: a symlinked spelling of a tree loads as the real path,
// and the machine-local store keys both spellings alike, so records,
// the evidence root, and the store never split one tree in two.
func TestLoadCanonicalizesTheTreeRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a temporary module")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	base := t.TempDir()
	// The real tree sits one level down so a `..` through the link
	// distinguishes the element-wise walk (the real parent, deep) from a
	// lexical clean (the link's parent, base).
	real := filepath.Join(base, "deep", "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go.mod": "module example.com/canon\n\ngo 1.26\n",
		"c.go":   "package canon\n\nfunc C() int { return 1 }\n",
	} {
		if err := os.WriteFile(filepath.Join(real, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	if tree.dir != canonical {
		t.Fatalf("tree root = %q, want the one coordinate %q", tree.dir, canonical)
	}
	_, viaLink, err := machineLocalDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, viaReal, err := machineLocalDir(real); err != nil || viaLink != viaReal {
		t.Fatalf("store keys differ: %q vs %q (%v)", viaLink, viaReal, err)
	}
	// A root that does not exist is refused as the tree root — in
	// gomutant's voice — at the load and at the store alike, before
	// anything is read or keyed.
	missing := filepath.Join(base, "missing")
	if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), "tree root") {
		t.Fatalf("load of a missing root = %v, want the tree-root refusal", err)
	}
	if _, err := OpenStore(filepath.Join(missing, ".gomutant", "findings.json"), missing); err == nil || !strings.HasPrefix(err.Error(), "gomutant: tree root ") {
		t.Fatalf("store over a missing root = %v, want the tree-root refusal itself, before the store resolves anything", err)
	}
	// The guards name the load's coordinate: a `..` through the link
	// reaches the real tree's parent (never the link's, as a lexical
	// clean would), and an unresolvable root still gets an absolute
	// spelling so the guard answers under it.
	// Spelled by concatenation: filepath.Join would clean the `..` away
	// lexically before the walk ever saw the link.
	if got := rootCoordinate(link + string(os.PathSeparator) + ".."); got != filepath.Dir(canonical) {
		t.Fatalf("rootCoordinate(link/..) = %q, want the real parent %q", got, filepath.Dir(canonical))
	}
	t.Chdir(base)
	if got := rootCoordinate("missing"); got != missing {
		t.Fatalf("rootCoordinate(missing, relative) = %q, want the absolute spelling %q", got, missing)
	}
	// The standalone toolchain guard names the load's coordinate: the
	// directory it hands the sampler for `link/..` is the real parent
	// (deep), never the link's lexical parent (base) — observed through
	// the sampler seam, since a host's GOTOOLCHAIN=local ignores any
	// directive a fixture could plant.
	var sampledIn string
	restore := engine.SwapGoVersionSamplerForTest(func(_ context.Context, dir string, _ []string) (string, error) {
		sampledIn = dir
		return runtime.Version(), nil
	})
	defer restore()
	if err := CheckToolchainProvenance(context.Background(), link+string(os.PathSeparator)+"..", Selection{}); err != nil {
		t.Fatalf("the guard over link/..: %v", err)
	}
	if sampledIn != filepath.Dir(canonical) {
		t.Fatalf("the guard sampled in %q, want the load's coordinate %q", sampledIn, filepath.Dir(canonical))
	}
}
