package engine

import "testing"

// Intersects answers the range-shaped bucket question with half-open
// semantics on both sides: a mutant extent beginning exactly at an
// executed block's end-exclusive boundary (the go1.27 brace shape —
// the body span starts after the brace, the condition span ends at
// it) intersects the BODY it spans, never the condition it merely
// touches. The point probe's boundary miss is the regression this
// pins against.
func TestCoverageIntersectsHalfOpenBoundaries(t *testing.T) {
	c := Coverage{covered: map[string][]coverSpan{
		"p/lib.go": {
			{startLine: 24, startCol: 2, endLine: 24, endCol: 12}, // if-condition, executed
			{startLine: 25, startCol: 3, endLine: 26, endCol: 1},  // body, executed
		},
	}}
	// The brace-anchored block extent (24:12 through the closing
	// brace) spans the executed body: intersects.
	if !c.Intersects("p/lib.go", 24, 12, 26, 3) {
		t.Fatal("body-spanning extent missed the executed body block")
	}
	// The same extent against a coverage where only the CONDITION ran
	// (branch never taken): the condition block ends exactly where the
	// extent begins — half-open on both sides, no intersection, the
	// honest never-executed.
	cond := Coverage{covered: map[string][]coverSpan{
		"p/lib.go": {{startLine: 24, startCol: 2, endLine: 24, endCol: 12}},
	}}
	if cond.Intersects("p/lib.go", 24, 12, 26, 3) {
		t.Fatal("an extent starting at a block's end-exclusive boundary claimed the block")
	}
	// One-column overlap is enough.
	if !cond.Intersects("p/lib.go", 24, 11, 24, 12) {
		t.Fatal("a one-column overlap missed")
	}
	// Zero-width and unknown-file extents never intersect.
	if cond.Intersects("p/lib.go", 24, 12, 24, 12) || cond.Intersects("p/other.go", 24, 2, 26, 3) {
		t.Fatal("degenerate extents claimed coverage")
	}
}
