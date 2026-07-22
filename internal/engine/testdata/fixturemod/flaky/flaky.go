// Package flaky exposes an oracle whose failure does not reproduce on
// a second run, exercising serial kill confirmation.
package flaky

// Value exists to be mutated.
func Value() int { return 7 }
