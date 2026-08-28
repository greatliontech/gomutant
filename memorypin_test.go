package gomutant

import "testing"

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
