package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A baseline probe whose test package fails to build refuses with the
// compiler's own diagnostic in the error (REQ-exec-ephemeral).
func TestProbeBuildFailureNamesCompilerDiagnostic(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/broken\n\ngo 1.24\n",
		"lib.go":      "package broken\n\nfunc Value() int { return 1 }\n",
		"lib_test.go": "package broken\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Undefined() != 1 { t.Fail() } }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := TestProbe(context.Background(), dir, "example.com/broken", "^TestValue$", time.Minute, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to build") {
		t.Fatalf("broken test package probe = %v, want a build refusal", err)
	}
	if !strings.Contains(err.Error(), "undefined") {
		t.Fatalf("build refusal lacks the compiler diagnostic: %v", err)
	}
}

// compileDiagnostics keeps the compiler's text from both streams — raw
// stderr, build-output events, and non-JSON lines in the -json stream —
// and caps a pathological diagnostic.
func TestCompileDiagnosticsExtraction(t *testing.T) {
	stdout := []byte(`{"Action":"start","Package":"p"}
{"Action":"build-output","Output":"p/f.go:3:2: undefined: q\n"}
# plain interleaved line
{"Action":"fail","Package":"p"}`)
	stderr := []byte("# example.com/p\nvet: something\n")
	got := compileDiagnostics(stdout, stderr)
	for _, want := range []string{"undefined: q", "# plain interleaved line", "# example.com/p", "vet: something"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostics = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, `"Action"`) {
		t.Fatalf("diagnostics leak raw JSON events: %q", got)
	}
	long := compileDiagnostics(nil, []byte(strings.Repeat("x", 10000)))
	if len(long) > 5000 || !strings.Contains(long, "[diagnostic truncated]") {
		t.Fatalf("diagnostic cap missing: len=%d", len(long))
	}
}
