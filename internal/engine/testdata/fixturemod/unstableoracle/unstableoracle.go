// Package unstableoracle pairs a stable test with one whose own run
// reads an ephemeral external input, exercising oracle-instability
// attribution.
package unstableoracle

// Value reports fixture health.
func Value() int { return 1 }
