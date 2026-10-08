package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A memory limit is also an ordinary runtime input when an oracle reads it.
// The directional resource-budget comparison cannot replace that value guard.
func TestMemoryReadingOracleServesOnlyTheDeliveredValue(t *testing.T) {
	if testing.Short() {
		t.Skip("measures and serves a memory-reading oracle")
	}
	dir := t.TempDir()
	for name, contents := range map[string]string{
		"go.mod": "module example.com/memory\n\ngo 1.26\n",
		"value.go": `package memory
//gofresh:pure
func Value(x int) int { if x > 100 { return x - 1 }; return x }
`,
		"value_test.go": `package memory
import ("os"; "testing")
//gofresh:pure
func TestValue(t *testing.T) {
 if os.Getenv("GOMEMLIMIT") == "" { t.Fatal("missing resource environment") }
 if Value(1) != 1 { t.Fatal("wrong value") }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "example.com/memory.Value", Oracle: []string{"example.com/memory.TestValue"}}}
	measure := func(memory int64, prior []Finding) []Finding {
		t.Helper()
		var decisions []RunDecision
		got, err := tr.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1, OracleMemoryBytes: memory, Prior: prior, Decision: func(d RunDecision) { decisions = append(decisions, d) }})
		if err != nil || len(got) != 1 || got[0].Skipped != "" {
			t.Fatalf("run: %v %v decisions=%v", got, err, decisions)
		}
		return got
	}
	first := measure(2<<30, nil)
	warm := measure(2<<30, first)
	if !warm[0].Cached {
		t.Fatal("same delivered memory value did not serve")
	}
	wider := measure(3<<30, warm)
	if wider[0].Cached {
		t.Fatal("a wider budget concealed a changed GOMEMLIMIT read")
	}
	settled := measure(3<<30, wider)
	if !settled[0].Cached {
		t.Fatal("remeasured memory value did not serve")
	}
}

// The oracle-memory pin is directional for records the ceiling never
// decided — a current ceiling at least as large as the recorded one
// preserves every verdict — and exact for ceiling-decided records,
// whose verdicts the ceiling authored (REQ-result-stale,
// REQ-exec-oracle-memory).
func TestMemoryPinStaleIsDirectional(t *testing.T) {
	record := func(bytes int64, decided bool) Finding {
		return Finding{OracleMemoryBytes: bytes, OracleCeilingDecided: decided}
	}
	cases := []struct {
		name    string
		prior   Finding
		current int64
		stale   bool
	}{
		{"equal ceiling serves", record(1<<30, false), 1 << 30, false},
		{"larger current ceiling serves", record(1<<30, false), 2 << 30, false},
		{"unlimited current serves any record", record(1<<30, false), 0, false},
		{"smaller current ceiling re-measures", record(2<<30, false), 1 << 30, true},
		{"bounded current against unlimited record re-measures", record(0, false), 1 << 30, true},
		{"unlimited both sides serves", record(0, false), 0, false},
		{"ceiling-decided pins exact: equal serves", record(1<<30, true), 1 << 30, false},
		{"ceiling-decided pins exact: larger re-measures", record(1<<30, true), 2 << 30, true},
		{"ceiling-decided pins exact: smaller re-measures", record(2<<30, true), 1 << 30, true},
		{"ceiling-decided pins exact: unlimited re-measures", record(1<<30, true), 0, true},
	}
	for _, tc := range cases {
		if got := memoryPinStale(tc.prior, tc.current); got != tc.stale {
			t.Errorf("%s: memoryPinStale = %v, want %v", tc.name, got, tc.stale)
		}
	}
}
