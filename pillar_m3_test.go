package gomutant

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPillarM3OracleMemoryBytesFromMiBOverflowGuard(t *testing.T) {
	if got := OracleMemoryBytesFromMiB(-1); got != -1 {
		t.Fatalf("OracleMemoryBytesFromMiB(-1) = %d, want -1", got)
	}
	if got := OracleMemoryBytesFromMiB(0); got != 0 {
		t.Fatalf("OracleMemoryBytesFromMiB(0) = %d, want 0", got)
	}
	if got := OracleMemoryBytesFromMiB(512); got != 512<<20 {
		t.Fatalf("OracleMemoryBytesFromMiB(512) = %d, want %d", got, int64(512)<<20)
	}
	for _, huge := range []int64{(math.MaxInt64 >> 20) + 1, math.MaxInt64 >> 10, math.MaxInt64} {
		if got := OracleMemoryBytesFromMiB(huge); got != math.MaxInt64 {
			t.Fatalf("OracleMemoryBytesFromMiB(%d) = %d, want math.MaxInt64 (%d)", huge, got, int64(math.MaxInt64))
		}
	}
}

func TestPillarM3ExplicitTestdataPackageSelection(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/tdmod\n\ngo 1.24\n",
		"lib.go": "package tdmod\n\nfunc HostAdd(a, b int) int { return a + b }\n",
		"lib_test.go": `package tdmod

import "testing"

func TestHostAdd(t *testing.T) {
	if HostAdd(2, 3) != 5 {
		t.Fatal("bad sum")
	}
}
`,
		"testdata/linux-init/main.go": `package main

func ValidateACPITableHeaderFields(sum byte) bool {
	if sum != 0 {
		return false
	}
	return true
}

func main() {}
`,
		"testdata/linux-init/main_test.go": `package main

import "testing"

func TestValidateACPITableHeaderFields(t *testing.T) {
	if !ValidateACPITableHeaderFields(0) {
		t.Fatal("zero checksum rejected")
	}
	if ValidateACPITableHeaderFields(1) {
		t.Fatal("non-zero checksum accepted")
	}
}
`,
	}
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	tree, err := LoadContext(ctx, dir)
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	// Default discovery excludes testdata packages, and package filters do not load them.
	discovered, err := tree.DiscoverContext(ctx)
	if err != nil {
		t.Fatalf("DiscoverContext: %v", err)
	}
	for _, tgt := range discovered {
		if strings.Contains(tgt.Symbol, "testdata") {
			t.Fatalf("default discovery included testdata symbol %s", tgt.Symbol)
		}
	}
	if _, err := tree.SelectTargets(ctx, SelectionRequest{
		Packages: []string{"example.com/tdmod/testdata/**"},
	}); err == nil || !strings.Contains(err.Error(), "matched no targets") {
		t.Fatalf("package filter on unloaded testdata = %v, want matched no targets refusal", err)
	}

	// Tree-escape and nonexistent testdata directory refusals remain intact.
	if _, err := tree.resolveTestPackage("../outside"); err == nil || !strings.Contains(err.Error(), "escapes the tree root") {
		t.Fatalf("resolveTestPackage(../outside) = %v, want tree-escape refusal", err)
	}
	if _, err := tree.resolveTestPackage("./testdata/nonexistent"); err == nil || !strings.Contains(err.Error(), "holds no loaded package") {
		t.Fatalf("resolveTestPackage(./testdata/nonexistent) = %v, want holds no loaded package refusal", err)
	}

	// Both relative directory and full import path spellings resolve and load the explicit testdata package.
	for _, spelling := range []string{"./testdata/linux-init", "example.com/tdmod/testdata/linux-init"} {
		tr, err := LoadContext(ctx, dir)
		if err != nil {
			t.Fatalf("LoadContext(%s): %v", spelling, err)
		}
		res, err := tr.RunEphemeral(ctx, EphemeralRequest{
			BatchEdits: []BatchEdit{{
				File:      "testdata/linux-init/main.go",
				OldString: "if sum != 0 {",
				NewString: "if false {",
			}},
			TestPkg:       spelling,
			Run:           "^TestValidateACPITableHeaderFields$",
			OracleTimeout: 30 * time.Second,
			Runs:          1,
		})
		if err != nil {
			t.Fatalf("RunEphemeral(%s): %v", spelling, err)
		}
		if !res.Killed || res.KilledRuns != 1 {
			t.Fatalf("RunEphemeral(%s) = %+v, want killed", spelling, res)
		}
		// Unselected test pattern still refuses before measurement.
		if _, err := tr.RunEphemeral(ctx, EphemeralRequest{
			BatchEdits: []BatchEdit{{
				File:      "testdata/linux-init/main.go",
				OldString: "if sum != 0 {",
				NewString: "if false {",
			}},
			TestPkg:       spelling,
			Run:           "^TestDoesNotExist$",
			OracleTimeout: 30 * time.Second,
		}); err == nil || !strings.Contains(err.Error(), "matched no tests") {
			t.Fatalf("unselected run pattern (%s) = %v, want matched no tests refusal", spelling, err)
		}
	}

	// Explicit campaign target selection loads the testdata target and oracle from module root.
	campTree, err := LoadContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := campTree.SelectTargets(ctx, SelectionRequest{
		TargetsGiven: true,
		Targets: []Target{{
			Symbol: "example.com/tdmod/testdata/linux-init.ValidateACPITableHeaderFields",
			Oracle: []string{"example.com/tdmod/testdata/linux-init.TestValidateACPITableHeaderFields"},
		}},
	})
	if err != nil || len(sel.Targets) != 1 {
		t.Fatalf("SelectTargets explicit testdata target = %+v, %v", sel, err)
	}
}
