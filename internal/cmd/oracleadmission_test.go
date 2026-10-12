package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandsRefuseInheritedOracleSkipBeforeLoad(t *testing.T) {
	dir := unloadable(t)
	replacement := filepath.Join(t.TempDir(), "p.go")
	if err := os.WriteFile(replacement, []byte("package p\nfunc Value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte("package p\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []string{"-skip=TestKiller", "--test.skip=TestKiller"} {
		t.Run(flags, func(t *testing.T) {
			t.Setenv("GOFLAGS", flags)
			for verb, err := range map[string]error{
				"run":       runCommand(context.Background(), runOptions{dir: dir, output: io.Discard}),
				"ephemeral": ephemeralCommand(context.Background(), ephemeralOptions{dir: dir, file: "p.go", replacement: replacement, testPkg: "example.com/p", runPat: "^TestKiller$", runs: 1}),
			} {
				if err == nil || !strings.Contains(err.Error(), "GOFLAGS skip") {
					t.Fatalf("%s: %v", verb, err)
				}
			}
		})
	}
}
