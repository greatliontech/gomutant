// Package counting exposes a deliberately weak oracle whose test
// appends one line per execution to an env-named file, pinning the
// exactly-once mutant-execution contract.
package counting

// Value exists to be mutated.
func Value() int { return 7 }
