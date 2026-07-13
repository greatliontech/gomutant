package lib

import "fmt"

// PruneCollision has consecutive removal sites followed by a non-removal
// site, allowing effective-source deduplication to be tested under a budget.
func PruneCollision() int {
	fmt.Sprintln("first")
	fmt.Sprintln("second")
	return 1
}
