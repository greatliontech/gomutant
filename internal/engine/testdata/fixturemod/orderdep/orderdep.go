// Package orderdep exposes an ORDER-DEPENDENT killer: the killing
// test needs a sibling test's setup and fails standalone regardless
// of the mutant. A killer-scoped confirmation without a scoped
// baseline would convert its sibling-shaped false kill into a
// confirmed kill; the scoped baseline refuses the scope and the full
// serial oracle flips it to a survivor.
package orderdep

// Value exists to be mutated.
func Value() int { return 7 }
