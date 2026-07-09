package lib

import "fmt"

// Logs's only statement is this file's sole reference to fmt: deleting it
// orphans the import, which the generator must prune so the mutant compiles.
func Logs() {
	fmt.Sprintln("logged")
}
