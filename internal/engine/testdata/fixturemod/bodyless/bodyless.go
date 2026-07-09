// Package bodyless declares a function without a body (implemented in
// assembly): its hashable source is the whole declaration.
package bodyless

func Ext(x int) int

// FmtA and FmtB have canonically identical bodies spelled with different
// formatting: their body hashes must be equal.
func FmtA(x int) int { return x + 1 }

func FmtB(x int) int {
	return x +
		1
}
