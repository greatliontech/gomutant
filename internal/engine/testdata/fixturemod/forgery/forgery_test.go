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
