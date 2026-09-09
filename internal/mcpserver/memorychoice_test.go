package mcpserver

import (
	"context"
	"os"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The caller's memory choice reaches the probe as the choice and is
// derived into a ceiling exactly once, so the two faces agree:
// oracle_memory_mib -1 is unlimited (as the CLI's -1 is), an explicit
// count is that ceiling, and an absent choice derives the default
// (REQ-exec-oracle-memory).
func TestEphemeralMemoryChoiceIsDerivedOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	probe := func(mib *int64) int64 {
		t.Helper()
		_, out, err := s.toolEphemeral(context.Background(), nil, ephemeralIn{
			File: "lib/lib.go", Edits: []gomutant.Edit{{Old: "return x - 1", New: "return x - 2"}},
			TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeoutSec: 60, OracleMemoryMiB: mib,
		})
		if err != nil {
			t.Fatal(err)
		}
		return out.OracleMemoryBytes
	}
	unlimited, explicit := int64(-1), int64(512)
	if got := probe(&unlimited); got != 0 {
		t.Fatalf("oracle_memory_mib -1 ran under ceiling %d, want unlimited (0)", got)
	}
	if got := probe(&explicit); got != 512<<20 {
		t.Fatalf("oracle_memory_mib 512 ran under ceiling %d, want 512 MiB", got)
	}
	if got := probe(nil); got <= 0 {
		t.Fatalf("an absent choice ran under ceiling %d, want the derived default", got)
	}
}
