// Package candlocal is fixture code whose oracle kills exactly one mutant by
// panicking mid-test — an observation the process cannot finalize — while
// every other mutant is decided by an ordinary assertion. It deliberately
// touches no runtime input surface, so its completed observations stay
// verifiable and reusable.
package candlocal

// Value exists for mutation testing: TestValue panics only when the result
// is zero, so exactly the zero-return mutant's test process cannot prove its
// runtime-input log complete.
func Value(a int) int {
	return a + 1
}
