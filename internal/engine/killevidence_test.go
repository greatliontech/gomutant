package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func jsonStream(t *testing.T, events []map[string]string) []byte {
	t.Helper()
	var b strings.Builder
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// Kill evidence is the killing test's own bounded output TAIL — Go
// emits the failure block last, so the excerpt anchors at the end:
// only the killer's lines (subtests folded to their top-level test),
// capped in line count and rune-safe line length with the dropped
// earlier remainder counted, other tests' output never leaking in.
func TestKillEvidenceKillerOutputTail(t *testing.T) {
	var events []map[string]string
	events = append(events, map[string]string{"Action": "output", "Package": "example.com/p", "Test": "TestOther", "Output": "other noise\n"})
	for i := 0; i < killerOutputLines+5; i++ {
		events = append(events, map[string]string{
			"Action": "output", "Package": "example.com/p", "Test": "TestX/case",
			"Output": fmt.Sprintf("=== RUN   TestX/case_%d\n", i),
		})
	}
	events = append(events, map[string]string{"Action": "output", "Package": "example.com/p", "Test": "TestX", "Output": strings.Repeat("héllo ", (killerOutputLineCap/6)+10) + "\n"})
	events = append(events, map[string]string{"Action": "output", "Package": "example.com/p", "Test": "TestX", "Output": "--- FAIL: TestX (0.00s)\n"})
	events = append(events, map[string]string{"Action": "output", "Package": "example.com/p", "Test": "TestX", "Output": "    x_test.go:9: the assertion text\n"})
	got := killEvidence(jsonStream(t, events), "example.com/p.TestX", time.Minute)
	lines := strings.Split(got, "\n")
	if len(lines) != killerOutputLines+1 {
		t.Fatalf("evidence lines = %d, want the cap plus the counted remainder:\n%s", len(lines), got)
	}
	if lines[0] != "(8 earlier output lines dropped)" {
		t.Fatalf("remainder = %q, want the dropped earlier-line count first", lines[0])
	}
	// The failure block Go emits last must survive the cap — that is
	// the evidence's whole purpose.
	if !strings.Contains(got, "--- FAIL: TestX") || !strings.Contains(got, "the assertion text") {
		t.Fatalf("failure block buried by the cap:\n%s", got)
	}
	if strings.Contains(got, "other noise") {
		t.Fatalf("another test's output leaked into the evidence:\n%s", got)
	}
	for _, l := range lines {
		if len(l) > killerOutputLineCap+3 {
			t.Fatalf("line exceeds the cap: %d chars", len(l))
		}
		if !utf8.ValidString(l) {
			t.Fatalf("line truncation split a rune: %q", l)
		}
	}
}

// A timeout verdict's evidence names the governing option; a
// package-scope kill carries the package-level output; survivors and
// discards carry nothing.
func TestKillEvidenceTimeoutAndPackageArms(t *testing.T) {
	if got := killEvidence(nil, TimeoutKiller, 90*time.Second); !strings.Contains(got, "oracle_timeout_sec") || !strings.Contains(got, "1m30s") {
		t.Fatalf("timeout evidence = %q, want the governing option and the bound", got)
	}
	stream := jsonStream(t, []map[string]string{
		{"Action": "output", "Package": "example.com/p", "Test": "", "Output": "panic: registry wiring exploded\n"},
		{"Action": "output", "Package": "example.com/p", "Test": "TestX", "Output": "test-attributed line\n"},
	})
	got := killEvidence(stream, PackageKillerPrefix+"example.com/p)", time.Minute)
	if !strings.Contains(got, "panic: registry wiring exploded") || strings.Contains(got, "test-attributed line") {
		t.Fatalf("package-scope evidence = %q, want package-level output only", got)
	}
}
