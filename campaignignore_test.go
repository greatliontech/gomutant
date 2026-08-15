//go:build unix

package gomutant

import (
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
	if err != nil || !strings.Contains(string(content), "*.campaign") {
		t.Fatalf("minted ignore = %q, %v; want the campaign-lock pattern", content, err)
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
	if !strings.Contains(string(content), "keep-me\n") || strings.Count(string(content), "*.campaign") != 1 {
		t.Fatalf("seeded ignore = %q, want existing content newline-separated and one appended pattern", content)
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
}
