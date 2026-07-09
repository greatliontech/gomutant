package badtest

import "testing"

// TestBad has a runnable-looking name but a signature go test rejects
// ("wrong signature"): never runnable, never an oracle.
func TestBad(t *testing.T) int { return 0 }
