// Package diskread models the disk-walking oracle family: its test
// derives part of its verdict from re-reading the package's own source
// bytes off disk, which the build overlay never touches.
package diskread

func D(x int) int {
	if x > 100 {
		return x - 1
	}
	return x
}
