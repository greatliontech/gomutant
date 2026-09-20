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

// Mixed is Value with a branch TestMixed never exercises: its zero-return
// mutant is candidate-local exactly as Value's, and the branch's mutants
// survive, so a record of it serves with survivors to bucket beside the
// re-executed candidate.
func Mixed(a int) int {
	if a > 1000 {
		return a - 1
	}
	return a + 1
}
