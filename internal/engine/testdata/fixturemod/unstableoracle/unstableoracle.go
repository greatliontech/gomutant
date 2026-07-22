// Package unstableoracle pairs a stable test with one whose own run
// reads an ephemeral external input, exercising oracle-instability
// attribution.
package unstableoracle

// Value reports fixture health.
func Value() int { return 1 }

// Weakly leaves its large-x branch untested so mutants there survive.
func Weakly(x int) int {
	if x > 100 {
		return x - 1
	}
	return x
}
