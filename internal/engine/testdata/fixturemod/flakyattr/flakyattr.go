// Package flakyattr exposes an oracle whose kill is cleanly
// TEST-ATTRIBUTED on the first look and does not reproduce on the
// second — the killer-scoped confirmation's flip path: the scoped
// serial run passes, and the verdict must come from the full serial
// oracle, never from the scoped run alone.
package flakyattr

// Value exists to be mutated.
func Value() int { return 7 }
