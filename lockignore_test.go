//go:build unix

package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tool-owned document directory carries a minted ignore for the
// by-design persistent campaign lock and the server's exit log, so
// add-everything staging loops cannot commit them; existing ignore
// content is preserved, the mint is idempotent, and user-owned
// directories stay untouched.
func TestCampaignLockIgnoreMinted(t *testing.T) {
	owned := filepath.Join(t.TempDir(), ".gomutant")
	release, err := AcquireCampaignLock(filepath.Join(owned, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	content, err := os.ReadFile(filepath.Join(owned, ".gitignore"))
	if err != nil || !strings.Contains(string(content), "*.campaign") || !strings.Contains(string(content), "*.lock") || !strings.Contains(string(content), ExitLogName+"\n") || !strings.Contains(string(content), ExitLogRotatedName+"\n") {
		t.Fatalf("minted ignore = %q, %v; want both persistent-lock patterns and the exit log with its kept generation", content, err)
	}

	seeded := filepath.Join(t.TempDir(), ".gomutant")
	if err := os.MkdirAll(seeded, 0o755); err != nil {
		t.Fatal(err)
	}
	// No trailing newline: the append must not fuse onto the existing
	// pattern (keep-me*.campaign would corrupt both lines).
	if err := os.WriteFile(filepath.Join(seeded, ".gitignore"), []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // second acquisition must not duplicate the pattern
		release, err := AcquireCampaignLock(filepath.Join(seeded, "findings.json"))
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	content, err = os.ReadFile(filepath.Join(seeded, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "keep-me\n") || strings.Count(string(content), "*.campaign") != 1 || strings.Count(string(content), "*.lock") != 1 {
		t.Fatalf("seeded ignore = %q, want existing content newline-separated and each pattern appended once", content)
	}

	// The upgrade path every pre-existing tool-owned directory is in: an
	// ignore minted before the exit log's names existed gains only the
	// missing patterns, each once.
	partial := filepath.Join(t.TempDir(), ".gomutant")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, ".gitignore"), []byte("*.campaign\n*.lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err = AcquireCampaignLock(filepath.Join(partial, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	content, err = os.ReadFile(filepath.Join(partial, ".gitignore"))
	if err != nil || strings.Count(string(content), "*.campaign") != 1 || strings.Count(string(content), "*.lock") != 1 || strings.Count(string(content), ExitLogName+"\n") != 1 || strings.Count(string(content), ExitLogRotatedName+"\n") != 1 {
		t.Fatalf("partial ignore = %q, %v; want the missing patterns appended once without duplicating the present ones", content, err)
	}

	foreign := t.TempDir()
	release, err = AcquireCampaignLock(filepath.Join(foreign, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(foreign, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("user-owned directory gained a minted ignore (stat err %v)", err)
	}

	// The document lock mints on its own: a write verb (disposition,
	// prune) on a fresh tool-owned directory persists findings.json.lock
	// with no campaign ever run, and the ignore must cover it before an
	// add-everything staging loop can commit it.
	docOnly := filepath.Join(t.TempDir(), ".gomutant")
	relDoc, err := acquireDocumentLock(context.Background(), filepath.Join(docOnly, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	relDoc()
	content, err = os.ReadFile(filepath.Join(docOnly, ".gitignore"))
	if err != nil || !strings.Contains(string(content), "*.lock") || !strings.Contains(string(content), "*.campaign") {
		t.Fatalf("document-lock-only mint = %q, %v; want both patterns", content, err)
	}
}

// The server's exit log and its kept generation live beside the default
// findings document under the tree root, whatever document a run
// serves — the paths the run adds to its own writes (REQ-mcp-exit-log).
func TestExitLogPathsSitBesideTheDefaultDocument(t *testing.T) {
	dir := t.TempDir()
	paths := ExitLogPaths(dir)
	if len(paths) != 2 || filepath.Base(paths[0]) != ExitLogName || filepath.Base(paths[1]) != ExitLogRotatedName || filepath.Dir(paths[0]) != filepath.Dir(FindingsPathAt(dir, "")) || filepath.Dir(paths[1]) != filepath.Dir(paths[0]) {
		t.Fatalf("exit log paths = %v; want the log and its generation beside the default document", paths)
	}
	// The store's own paths are those two and the minted ignore.
	if own := StoreOwnPaths(dir); len(own) != 3 || own[0] != paths[0] || own[1] != paths[1] || own[2] != filepath.Join(filepath.Dir(paths[0]), ".gitignore") {
		t.Fatalf("store own paths = %v", own)
	}
}
