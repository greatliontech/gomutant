package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/tools/cover"
)

// Coverage is one or more baseline coverage profiles' executed extents,
// queryable by import-path-qualified file position.
type Coverage struct {
	covered map[string][]coverSpan
}

// coverSpan is one executed coverage block's extent.
type coverSpan struct {
	startLine, startCol, endLine, endCol int
}

// CoveredPositions runs the oracle's baseline once with coverage
// instrumentation over the target's package and reports the positions
// its tests reach. The profile is measured on the unmutated tree, so
// bucketing a survivor with it is advisory classification, never a
// measurement pin (REQ-exec-survivor-evidence).
func CoveredPositions(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, binFlags, env []string) (Coverage, error) {
	tmp, err := os.MkdirTemp("", "gomutant-cover-*")
	if err != nil {
		return Coverage{}, err
	}
	defer os.RemoveAll(tmp)
	profile := filepath.Join(tmp, "cover.out")
	tail := append([]string{"-count=1", "-run", runRegex, "-coverprofile", profile, "-coverpkg", coverPkg, testPkg}, binFlags...)
	args := goTestArgs(timeout, tail...)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := commandContext(runCtx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return Coverage{}, fmt.Errorf("coverage probe for %s under %s: %v: %s", coverPkg, testPkg, err, coverageTail(string(out), 300))
	}
	profiles, err := cover.ParseProfiles(profile)
	if err != nil {
		return Coverage{}, fmt.Errorf("parse coverage profile: %w", err)
	}
	covered := make(map[string][]coverSpan)
	for _, p := range profiles {
		for _, b := range p.Blocks {
			if b.Count == 0 {
				continue
			}
			covered[p.FileName] = append(covered[p.FileName], coverSpan{startLine: b.StartLine, startCol: b.StartCol, endLine: b.EndLine, endCol: b.EndCol})
		}
	}
	return Coverage{covered: covered}, nil
}

// Merge unions another profile's executed extents into this one.
func (c Coverage) Merge(other Coverage) Coverage {
	if c.covered == nil {
		c.covered = make(map[string][]coverSpan)
	}
	for file, spans := range other.covered {
		c.covered[file] = append(c.covered[file], spans...)
	}
	return c
}

// Covered reports whether the import-path-qualified file position falls
// inside any executed block.
func (c Coverage) Covered(qualifiedFile string, line, col int) bool {
	for _, s := range c.covered[qualifiedFile] {
		if (line > s.startLine || (line == s.startLine && col >= s.startCol)) &&
			(line < s.endLine || (line == s.endLine && col < s.endCol)) {
			return true
		}
	}
	return false
}

func coverageTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
