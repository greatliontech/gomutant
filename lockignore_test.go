//go:build unix

package gomutant

import (
	"context"
	"errors"
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
	if err != nil || string(content) != "*.campaign\n*.lock\n" {
		t.Fatalf("minted ignore = %q, %v; want exactly the two persistent-lock patterns (the exit log lives outside the tree)", content, err)
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
	if err != nil || strings.Count(string(content), "*.campaign") != 1 || strings.Count(string(content), "*.lock") != 1 || strings.Contains(string(content), ExitLogName) {
		t.Fatalf("partial ignore = %q, %v; want the missing pattern appended once without duplicating the present one, and no exit log line", content, err)
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

// The server's exit log and its kept generation live under the tree's
// machine-local state home — $XDG_STATE_HOME/gomutant/repos/ keyed as
// the cache home is, the one key both homes share — never under the
// tree; a relative state home is refused, an unset one is
// $HOME/.local/state on this host (the rule's platform table is
// TestStateHomeRuleCoversEveryPlatform), an unresolvable root refused;
// the store's own paths are the minted ignore alone; the decision map
// states the home's rule whole (REQ-mcp-exit-log).
func TestExitLogPathsLiveUnderTheStateHome(t *testing.T) {
	dir := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	paths, err := ExitLogPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, cacheDir, err := machineLocalDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(state, "gomutant", "repos", filepath.Base(cacheDir))
	if len(paths) != 2 || filepath.Dir(paths[0]) != want || filepath.Base(paths[0]) != ExitLogName || filepath.Dir(paths[1]) != want || filepath.Base(paths[1]) != ExitLogRotatedName {
		t.Fatalf("exit log paths = %v; want the log and its generation under %s", paths, want)
	}
	if rel, err := filepath.Rel(dir, paths[0]); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("the exit log %s lies under the served tree %s", paths[0], dir)
	}
	t.Setenv("XDG_STATE_HOME", "relative/state")
	if _, err := ExitLogPaths(dir); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("a relative state home: err = %v, want the refusal", err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", state)
	if paths, err := ExitLogPaths(dir); err != nil || filepath.Dir(filepath.Dir(filepath.Dir(paths[0]))) != filepath.Join(state, ".local", "state", "gomutant") {
		t.Fatalf("an unset state home = %v, %v; want $HOME/.local/state", paths, err)
	}
	// A tree root that does not resolve refuses the paths.
	if paths, err := ExitLogPaths(filepath.Join(dir, "absent")); err == nil {
		t.Fatalf("an unresolvable tree root answered %v, want the refusal", paths)
	}
	if own := StoreOwnPaths(dir); len(own) != 1 || own[0] != filepath.Join(filepath.Dir(FindingsPathAt(dir, "")), ".gitignore") {
		t.Fatalf("store own paths = %v, want the minted ignore alone", own)
	}
	// The decision map the served instructions carry states the home's
	// rule (REQ-mcp-guidance keeps the instructions the map verbatim).
	if o := Guidance().Orientation(); !strings.Contains(o, "$XDG_STATE_HOME/gomutant/repos/") || !strings.Contains(o, "`~/.local/state` where the variable is unset; on Windows") {
		t.Fatal("the decision map does not state the exit log's home rule whole")
	}
}

// The state home's rule over every platform: $XDG_STATE_HOME when set
// (absolute, on every platform), else $HOME/.local/state on every host
// but Windows — refused where HOME is unset — and on Windows, the one
// host whose cache home HOME does not root, the platform's own
// per-user cache directory, its failure refused (REQ-mcp-exit-log).
func TestStateHomeRuleCoversEveryPlatform(t *testing.T) {
	cache := func() (string, error) { return "/platform/cache", nil }
	noCache := func() (string, error) { return "", errors.New("no cache") }
	for _, tc := range []struct {
		goos, xdg, home string
		cache           func() (string, error)
		want, refusal   string
	}{
		{"linux", "/state", "/home/u", cache, "/state", ""},
		{"windows", "/state", "", noCache, "/state", ""},
		{"linux", "rel/state", "/home/u", cache, "", "relative"},
		{"linux", "", "/home/u", cache, "/home/u/.local/state", ""},
		{"freebsd", "", "/home/u", cache, "/home/u/.local/state", ""},
		{"linux", "", "", cache, "", "neither"},
		{"darwin", "", "/Users/u", cache, "/Users/u/.local/state", ""},
		{"darwin", "", "", cache, "", "neither"},
		{"windows", "", "", cache, "/platform/cache", ""},
		{"windows", "", "/home/u", cache, "/platform/cache", ""},
		{"windows", "", "", noCache, "", "no user state directory"},
	} {
		got, err := stateHome(tc.goos, tc.xdg, tc.home, tc.cache)
		if tc.refusal != "" {
			if err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("%+v: err = %v, want %q", tc, err, tc.refusal)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%+v: = %q, %v; want %q", tc, got, err, tc.want)
		}
	}
}
