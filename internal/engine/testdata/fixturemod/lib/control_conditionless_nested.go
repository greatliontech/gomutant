package lib

// The func-literal body's INTERNAL semicolon is load-bearing: a ';'
// at brace depth 1 inside the for header, which the conditionless-for
// scanner must skip to find the real depth-0 separator. An internal
// separator between two statements survives gofmt; a trailing one
// does not — the fixture must keep this exact shape under any
// formatting pass.
func SemicolonNestedCondition() {
	for function := func() { println(); println() }; ; {
		_ = function
		break
	}
}
