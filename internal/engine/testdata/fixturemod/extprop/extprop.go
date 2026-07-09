// Package extprop imports rapid only from its external test package: the
// rapid-split partition must still flag it.
package extprop

// Ok reports fixture health.
func Ok() bool { return true }
