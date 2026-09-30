package engine

import (
	"os"
	"testing"

	"github.com/greatliontech/gomutant/internal/fixtureguard"
)

// TestMain guards the tracked fixture trees: the tests that plant a
// file inside the fixture module do so in a copy (copiedFixture), and a
// residue before the run, or a test in this run writing into a tree —
// a member left, or created and removed — fails the suite naming the
// member: the root, cmd, and mcpserver suites read these trees in place
// beside this one, and a member appearing under their observation
// brackets seals their evidence as moved.
func TestMain(m *testing.M) {
	os.Exit(fixtureguard.Guard(os.Stderr, "testdata", m.Run))
}
