package gomutant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A self-overlapping pattern with more than one valid match start is
// ambiguous even when the non-overlapping count is one: applying it at
// a guessed start measures the wrong mutant (REQ-exec-ephemeral).
func TestEditUniquenessCountsOverlappingStarts(t *testing.T) {
	cases := []struct {
		s, pattern string
		want       int
	}{
		{"aaa", "aa", 2},
		{"abab", "ab", 2},
		{"abcabc", "bc", 2},
		{"unique", "unique", 1},
		{"none", "missing", 0},
		{"x", "", 0},
	}
	for _, tc := range cases {
		if got := overlappingMatchStarts(tc.s, tc.pattern); got != tc.want {
			t.Fatalf("starts(%q, %q) = %d, want %d", tc.s, tc.pattern, got, tc.want)
		}
	}

	// The sequential edit path refuses the overlap.
	if _, err := ApplyEdits([]byte("aaa"), []Edit{{Old: "aa", New: "zz"}}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("overlapping sequential edit = %v, want an ambiguity refusal", err)
	}

	// The batch path refuses it too, against real file bytes.
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lib", "lib.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(src, []byte("\n// marker: zzz\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.EphemeralBatch(t.Context(), []BatchEdit{{File: "lib/lib.go", OldString: "zz", NewString: "qq"}}, "example.com/fixture/lib", "^TestAdd$", 0)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("overlapping batch edit = %v, want an ambiguity refusal", err)
	}
}
