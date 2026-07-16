package observed

import (
	"os"
	"testing"
)

func TestObservedInput(_ *testing.T) {
	_, _ = os.ReadFile("input.txt")
}
