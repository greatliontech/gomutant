// Package forgery has a test that quotes a captured go-test build-failure
// line in its failure output, the way build-tooling suites do: mutant
// classification must never read that marker as a failed build.
package forgery

// Guarded reports fixture health.
func Guarded() bool { return true }
