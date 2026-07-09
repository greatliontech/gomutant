// Package dotx lives at an import path containing a dot: its symbols parse
// as "<...>/dot.x.F", which only longest-match splitting resolves.
package dotx

func F() int { return 2 }
