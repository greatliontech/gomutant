// Package g holds a guard whose error path is the only use of an
// import: deleting the guard whole strands the import.
package g

import "fmt"

// Check refuses a negative x.
func Check(x int) error {
	if x < 0 {
		return fmt.Errorf("negative %d", x)
	}
	return nil
}
