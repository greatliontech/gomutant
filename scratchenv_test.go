package gomutant

import (
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
)

// TestRebaseScratchEnvComposesTheWorkspaceOnce pins the scratch
// environment's workspace rebase to the policy's one setter: a
// tree-anchored GOWORK moves onto the scratch copy as exactly one
// entry, every other entry kept; an off or outside workspace stays
// (REQ-exec-spawn-environment).
func TestRebaseScratchEnvComposesTheWorkspaceOnce(t *testing.T) {
	got := rebaseScratchEnv([]string{"HOME=/h", "GOWORK=/real/nested/go.work", "A=1"}, "/real", "/scratch")
	if v, ok := gotool.LookupEnv(got, "GOWORK"); !ok || v != "/scratch/nested/go.work" {
		t.Fatalf("GOWORK = %q/%v, want the scratch copy's", v, ok)
	}
	if n := countEntries(got, "GOWORK"); n != 1 {
		t.Fatalf("GOWORK appears %d times in %v", n, got)
	}
	if !slices.Contains(got, "HOME=/h") || !slices.Contains(got, "A=1") || len(got) != 3 {
		t.Fatalf("other entries moved: %v", got)
	}
	for _, kept := range [][]string{{"GOWORK=off", "A=1"}, {"GOWORK=/elsewhere/go.work"}, {"A=1"}} {
		if got := rebaseScratchEnv(kept, "/real", "/scratch"); !slices.Equal(got, kept) {
			t.Fatalf("rebase of %v = %v, want untouched", kept, got)
		}
	}
}

func countEntries(env []string, key string) int {
	n := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			n++
		}
	}
	return n
}
