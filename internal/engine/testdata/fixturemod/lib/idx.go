package lib

// Idx exists for the build-failed discard: incrementing the constant index
// yields a[1] on a [1]int — a mutant that cannot compile and must be
// discarded, never scored.
func Idx() int {
	a := [1]int{7}
	return a[0]
}
