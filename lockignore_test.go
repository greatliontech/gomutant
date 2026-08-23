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
// by-design persistent campaign lock, so add-everything staging loops
// cannot commit it; existing ignore content is preserved, the mint is
// idempotent, and user-owned directories stay untouched.
func TestCampaignLockIgnoreMinted(t *testing.T) {
	owned := filepath.Join(t.TempDir(), ".gomutant")
	release, err := AcquireCampaignLock(filepath.Join(owned, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	content, err := os.ReadFile(filepath.Join(owned, ".gitignore"))
	if err != nil || !strings.Contains(string(content), "*.campaign") || !strings.Contains(string(content), "*.lock") {
		t.Fatalf("minted ignore = %q, %v; want both persistent-lock patterns", content, err)
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
	// ignore minted before *.lock existed gains only the missing pattern.
	partial := filepath.Join(t.TempDir(), ".gomutant")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, ".gitignore"), []byte("*.campaign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err = AcquireCampaignLock(filepath.Join(partial, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	content, err = os.ReadFile(filepath.Join(partial, ".gitignore"))
	if err != nil || strings.Count(string(content), "*.campaign") != 1 || strings.Count(string(content), "*.lock") != 1 {
		t.Fatalf("partial ignore = %q, %v; want the missing pattern appended without duplicating the present one", content, err)
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
