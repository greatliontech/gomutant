package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsRefuseInheritedOracleSkipBeforeLoad(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{
		"go.mod": "this is not a module file\n",
		"p.go":   "package p\nfunc Value() int { return 1 }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, flags := range []string{"-skip=TestKiller", "--test.skip=TestKiller"} {
		t.Run(flags, func(t *testing.T) {
			t.Setenv("GOFLAGS", flags)
			s := New(dir)
			_, _, runErr := s.toolRun(context.Background(), nil, runIn{})
			_, _, probeErr := s.toolEphemeral(context.Background(), nil, ephemeralIn{File: "p.go", Replacement: "package p\nfunc Value() int { return 2 }\n", TestPkg: "example.com/p", Run: "^TestKiller$", Runs: 1})
			for verb, err := range map[string]error{"run": runErr, "ephemeral": probeErr} {
				if err == nil || !strings.Contains(err.Error(), "GOFLAGS skip") {
					t.Fatalf("%s: %v", verb, err)
				}
			}
		})
	}
}
