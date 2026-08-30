package indirectprop

import (
	"testing"

	"example.com/fixture/rapidhelper"
)

func TestDoubleProperty(t *testing.T) {
	rapidhelper.Drive(t, func() bool { return Double(2) == 4 })
}
