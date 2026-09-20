// Package extinput reads a fixture outside the module through an
// environment-named absolute path, exercising caller-declared
// observation-bracket coverage.
package extinput

// Flag reports whether the fixture toggles behavior.
func Flag(on bool) int {
	if on {
		return 1
	}
	return 2
}

// Loose keeps survivors under TestFlag: its branch is never exercised,
// so a record of it re-measures survivors under the I/O-reading oracle.
func Loose(n int) int {
	if n > 10 {
		return n - 1
	}
	return n
}
