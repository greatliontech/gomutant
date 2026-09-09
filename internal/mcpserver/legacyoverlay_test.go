package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/legacytest"

	gomutant "github.com/greatliontech/gomutant"
)

// The run and findings tools carry preserved legacy overlay entries as
// rows of their own — path and version — so an MCP caller learns of the
// older binary's unserved records without reading a log; the list caps
// at the envelope's row bound with the remainder counted
// (REQ-result-layers, REQ-mcp-envelope).
func TestToolsCarryPreservedLegacyOverlays(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	versions := make([]int, envelope.rows+3)
	for i := range versions {
		versions[i] = 2
	}
	entries := legacytest.Plant(t, dir, filepath.Join(dir, gomutant.DefaultFindingsPath), versions...)
	planted := map[string]bool{}
	for _, e := range entries {
		planted[e] = true
	}
	s := New(dir)
	_, findings, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings.LegacyOverlays) != envelope.rows || findings.OmittedLegacyOverlays != 3 {
		t.Fatalf("findings legacy rows = %d, omitted %d; want %d and 3", len(findings.LegacyOverlays), findings.OmittedLegacyOverlays, envelope.rows)
	}
	for _, row := range findings.LegacyOverlays {
		if !planted[row.Path] || row.Version != 2 {
			t.Fatalf("findings legacy row %+v is not a planted version-2 entry", row)
		}
	}
	_, run, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"targets":[]}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.LegacyOverlays) != envelope.rows || run.OmittedLegacyOverlays != 3 {
		t.Fatalf("run legacy rows = %d, omitted %d; want %d and 3", len(run.LegacyOverlays), run.OmittedLegacyOverlays, envelope.rows)
	}
	for _, e := range entries {
		if _, err := os.Stat(e); err != nil {
			t.Fatalf("legacy entry destroyed: %v", err)
		}
	}
}
