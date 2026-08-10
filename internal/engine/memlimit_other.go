//go:build !linux

package engine

import "os/exec"

const memoryCeilingSupported = false

func totalRAMBytes() int64 { return 0 }

func applyMemoryCeiling(cmd *exec.Cmd, limit int64) {}
