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
