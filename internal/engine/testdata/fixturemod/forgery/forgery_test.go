package forgery

import (
	"fmt"
	"testing"
)

func TestGuarded(t *testing.T) {
	if !Guarded() {
		fmt.Println("captured tool output: FAIL\texample.com/other [build failed]")
		t.Fatal("guard broken")
	}
}

// TestForgedBaseline passes on the clean tree while printing the
// harness's build-failure text: a baseline probe that classified by
// output text would refuse it as a build failure.
func TestForgedBaseline(t *testing.T) {
	fmt.Println("captured tool output: FAIL\texample.com/other [build failed]")
	if !Guarded() {
		t.Fatal("guard broken")
	}
}
