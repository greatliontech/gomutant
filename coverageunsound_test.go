package gomutant

import (
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// TestSurvivorCoverageRefusesUnsoundFiles pins the empty-bucket
// posture end to end at the verdict layer: a file the directive seam
// refused to re-key (contention, collision) has NO coverage verdict —
// survivorCovered answers "not a coverage verdict" even when spans
// exist under the file's key, so the bucket stays empty instead of
// manufacturing never-executed for covered code or executed-and-passed
// from a polluted entry (REQ-exec-survivor-evidence's fail-closed
// posture).
func TestSurvivorCoverageRefusesUnsoundFiles(t *testing.T) {
	cov := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{
		"example.com/p/f.go": {{StartLine: 1, StartCol: 1, EndLine: 9, EndCol: 1}},
	})
	if covered, ok := survivorCovered(cov, "example.com/p", Survivor{Position: "f.go:2:3"}); !ok || !covered {
		t.Fatalf("sound file lost its verdict (covered=%v ok=%v)", covered, ok)
	}
	cov = cov.UnsoundForTest("example.com/p/f.go")
	if covered, ok := survivorCovered(cov, "example.com/p", Survivor{Position: "f.go:2:3"}); ok || covered {
		t.Fatalf("unsound file yielded a coverage verdict (covered=%v ok=%v): the bucket must stay empty", covered, ok)
	}
	// The extent-shaped probe refuses identically.
	s := Survivor{Position: "f.go:2:3", Extent: "2:3-4:1"}
	if covered, ok := survivorCovered(cov, "example.com/p", s); ok || covered {
		t.Fatalf("unsound file yielded an extent verdict (covered=%v ok=%v)", covered, ok)
	}
}
