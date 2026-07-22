package extinput

import (
	"os"
	"testing"
)

func TestFlag(t *testing.T) {
	if path := os.Getenv("GOMUTANT_EXTERNAL_FIXTURE"); path != "" {
		_, _ = os.ReadFile(path)
	}
	if Flag(true) != 1 || Flag(false) != 2 {
		t.Fatal("flag")
	}
}
