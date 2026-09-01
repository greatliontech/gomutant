package engine

import (
	"reflect"
	"testing"
)

// PersistedCoverage round-trips losslessly for every query Coverage
// answers: spans and unsound marks survive Persist/Restore exactly
// (REQ-result-baseline-bank's coverage banking).
func TestPersistedCoverageRoundTrip(t *testing.T) {
	c := CoverageForTest(map[string][]CoverSpanForTest{
		"example.com/p/f.go": {{StartLine: 1, StartCol: 2, EndLine: 3, EndCol: 4}, {StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}},
		"example.com/p/g.go": {{StartLine: 5, StartCol: 5, EndLine: 6, EndCol: 6}},
	}).UnsoundForTest("example.com/p/h.go")
	p := c.Persist()
	again := p.Restore().Persist()
	if !reflect.DeepEqual(p, again) {
		t.Fatalf("round trip diverged:\n%+v\nvs\n%+v", p, again)
	}
	if len(p.Unsound) != 1 || p.Unsound[0] != "example.com/p/h.go" {
		t.Fatalf("unsound marks lost: %+v", p.Unsound)
	}
	if got := len(p.Covered["example.com/p/f.go"]); got != 2 {
		t.Fatalf("spans lost: %d", got)
	}
}
