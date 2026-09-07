package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/legacytest"
)

// A findings document beside preserved legacy overlay entries names
// them on the human face before any row, and beside the document on
// the JSON face (stderr), never inside it (REQ-result-layers).
func TestFindingsCommandNamesPreservedLegacyOverlays(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := legacytest.Plant(t, dir, findingsAt(dir, defaultFindings), 2)[0]
	ctx := context.Background()
	var human bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings}, &human); err != nil {
		t.Fatal(err)
	}
	want := "machine-local overlay: 1 legacy entry preserved unread (document version 2; this binary reads"
	if !strings.HasPrefix(human.String(), want) || !strings.Contains(human.String(), filepath.Dir(entry)) {
		t.Fatalf("human face did not lead with the legacy line: %q", human.String())
	}
	var doc, notes bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, json: true, errOut: &notes}, &doc); err != nil {
		t.Fatal(err)
	}
	var rows []any
	if err := json.Unmarshal(doc.Bytes(), &rows); err != nil {
		t.Fatalf("JSON face polluted: %v: %q", err, doc.String())
	}
	if !strings.HasPrefix(notes.String(), want) {
		t.Fatalf("JSON face's notes = %q, want the legacy line", notes.String())
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("legacy entry destroyed by inspection: %v", err)
	}
}

// A run beside preserved legacy overlay entries names them on both
// faces — a human line, a note event on the structured stream — before
// its first preparation event; the line counts the entries and lists
// their versions ascending (REQ-result-layers).
func TestRunCommandNamesPreservedLegacyOverlays(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":     "module example.com/empty\n\ngo 1.26.4\n",
		"empty.go":   "package empty\n",
		"empty.json": `{"targets":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	docPath := filepath.Join(dir, "findings.json")
	legacytest.Plant(t, dir, docPath, 3, 2)
	want := "machine-local overlay: 2 legacy entries preserved unread (document version 2, 3; this binary reads"
	var human bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: filepath.Join(dir, "empty.json"), findingsFile: docPath, output: &human}); err != nil {
		t.Fatal(err)
	}
	// The run's identity leads; the legacy line follows it and precedes
	// the first preparation event.
	head, rest, ok := strings.Cut(human.String(), "\n")
	if !ok || !strings.HasPrefix(head, "run       ") || !strings.HasPrefix(rest, want) {
		t.Fatalf("human run face did not name the legacy entries right after the run line: %q", human.String())
	}
	var stream bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: filepath.Join(dir, "empty.json"), findingsFile: docPath, jsonl: true, output: &stream}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(stream.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("non-JSON line leaked: %q", line)
		}
		if env["event"] == "note" && strings.HasPrefix(fmt.Sprint(env["text"]), want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("structured run face carried no legacy note: %q", stream.String())
	}
}
