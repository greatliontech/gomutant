//go:build !linux

package engine

import "os/exec"

const memoryCeilingSupported = false

func applyMemoryCeiling(cmd *exec.Cmd, limit int64) {}

