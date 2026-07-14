package lib

// Dup has two identical same-line statements: deleting either renders the
// same source, so the generator's dedup must collapse them to one mutant.
func Dup() { sink(); sink() }

func sink() { counter++ }

var counter int

// Concat concatenates strings: the arithmetic swap must skip non-numeric
// operands, so no "+ -> -" mutant may appear here.
func Concat(a, b string) string {
	return a + b
}

// BigLit exercises arbitrary-precision integer-literal mutation.
func BigLit() uint64 {
	return 0xFFFFFFFFFFFFFFFF
}

func Loop(n int) int {
	for n < 3 {
		n++
	}
	return n
}
