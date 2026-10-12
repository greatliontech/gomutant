package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/go-x-tools/go/packages"
)

func TestOracleFlagsPreserveGoArgumentMeaning(t *testing.T) {
	for _, tc := range []struct {
		flags  string
		refuse bool
	}{
		{"", false},
		{"-skip=", false},
		{"--test.skip=", false},
		{"-skip=TestKiller", true},
		{"--test.skip=TestKiller", true},
		{"'-skip=TestKiller'", true},
		{"\"--test.skip=TestKiller\"", true},
		{"-skip=TestKiller --test.skip=", false},
		{"-test.skip= --skip=TestKiller", true},
		{"'-ldflags=-X p.value=-skip=TestKiller'", false},
		{"'-modfile=/tmp/two  spaces/alternate.mod' -tags=old", false},
		{"-skip", true},
		{"'-skip=TestKiller", true},
		{"--", true},
		{"---skip=TestKiller", true},
	} {
		t.Run(tc.flags, func(t *testing.T) {
			err := oracleFlags(tc.flags)
			if (err != nil) != tc.refuse {
				t.Fatalf("oracle flags %q = %v, refuse=%v", tc.flags, err, tc.refuse)
			}
		})
	}
	got, err := replaceTagsFlag("'-modfile=/tmp/two  spaces/alternate.mod' --tags=old -trimpath", "new")
	if err != nil || got != "'-modfile=/tmp/two  spaces/alternate.mod' -trimpath -tags=new" {
		t.Fatalf("replacement = %q, %v", got, err)
	}
	if _, err := (Selection{Tags: []string{"new"}}).applyEnv([]string{"GOFLAGS='-tags=old"}); err == nil {
		t.Fatal("selection swallowed malformed argument quoting")
	}
}

// Go's historical tag-value syntax is distinct from the outer GOFLAGS quoting.
// The same selected test must occur in Go's and both Gomutant inventories.
func TestQuotedBuildTagsMatchGoInventory(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and lists tag-selected fixtures")
	}
	for _, tc := range []struct {
		flags string
		want  []string
	}{
		{"'-tags=alpha beta'", []string{"alpha", "beta"}},
		{"-tags='alpha'", []string{"alpha"}},
		{"-tags=old '-tags=alpha beta'", []string{"alpha", "beta"}},
		{"-tags=alpha,,beta,", []string{"alpha", "beta"}},
	} {
		t.Run(tc.flags, func(t *testing.T) {
			t.Setenv("GOFLAGS", tc.flags)
			dir := t.TempDir()
			const pkg = "example.com/tagvalues"
			for name, text := range map[string]string{
				"go.mod":           "module " + pkg + "\n\ngo 1.27.0\n",
				"p.go":             "package p\n",
				"selected_test.go": "//go:build alpha\n\npackage p\nimport \"testing\"\nfunc TestSelected(t *testing.T) {}\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			out, err := goRunner.Run(ctx, dir, GoEnv(dir), "test", "-list=.", ".")
			if err != nil || !slices.Contains(strings.Split(string(out), "\n"), "TestSelected") {
				t.Fatalf("Go inventory: %s, %v", out, err)
			}
			tr, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			matcher, err := tr.buildMatchContext(ctx, dir)
			if err != nil || !slices.Equal(matcher.BuildTags, tc.want) {
				t.Fatalf("tags=%v error=%v, want %v", matcher.BuildTags, err, tc.want)
			}
			want := []string{pkg + ".TestSelected"}
			if !slices.Equal(tr.TestsOf(pkg), want) {
				t.Fatalf("loaded inventory: %v", tr.TestsOf(pkg))
			}
			if err := tr.VerifyTestEnumerationContext(ctx, pkg, want); err != nil {
				t.Fatalf("disk inventory: %v", err)
			}
		})
	}
}

// TestOracleSkipRefusesBeforePackageLoad exercises both the process environment
// and the go command's persistent configuration, without editing the host's.
func TestOracleSkipRefusesBeforePackageLoad(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/skip\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"environment", "file"} {
		for _, flags := range []string{"-skip=TestKiller", "--test.skip=TestKiller", "'-skip=TestKiller'"} {
			t.Run(source+flags, func(t *testing.T) {
				t.Setenv("GOFLAGS", "")
				t.Setenv("GOENV", "off")
				if source == "environment" {
					t.Setenv("GOFLAGS", flags)
				} else {
					path := filepath.Join(t.TempDir(), "go.env")
					if err := os.WriteFile(path, []byte("GOFLAGS="+flags+"\n"), 0o644); err != nil {
						t.Fatal(err)
					}
					t.Setenv("GOENV", path)
				}
				loaded := false
				_, err := loadContextWith(context.Background(), dir, Selection{}, true,
					func(*packages.Config, ...string) ([]*packages.Package, error) {
						loaded = true
						return nil, nil
					})
				if loaded || err == nil || !strings.Contains(err.Error(), "GOFLAGS skip") {
					t.Fatalf("loaded=%v error=%v", loaded, err)
				}
			})
		}
	}
}
