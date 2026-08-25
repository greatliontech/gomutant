package engine

import (
	"bytes"
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
	scratchEnv, _, _, removeScratch, err := oracleScratch(env)
	if err != nil {
		return Coverage{}, err
	}
	defer removeScratch()
	cmd.Env = oracleEnv(scratchEnv)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := runOracleProcess(cmd); err != nil {
		return Coverage{}, fmt.Errorf("coverage probe for %s under %s: %v: %s", coverPkg, testPkg, err, coverageTail(out.String(), 300))
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
// Intersects reports whether any executed block overlaps the
// half-open source range [start, end): the range-shaped question a
// mutant's execution bucket asks. Point containment alone sits on
// toolchain-dependent block boundaries — go1.27 moved body spans off
// the brace token, flipping brace-anchored mutants to never-executed
// while their bodies demonstrably ran.
func (c Coverage) Intersects(qualifiedFile string, startLine, startCol, endLine, endCol int) bool {
	before := func(l1, c1, l2, c2 int) bool { return l1 < l2 || (l1 == l2 && c1 < c2) }
	for _, s := range c.covered[qualifiedFile] {
		if before(startLine, startCol, s.endLine, s.endCol) && before(s.startLine, s.startCol, endLine, endCol) {
			return true
		}
	}
	return false
}

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

// CoversFile reports whether any executed block lives in the
// import-path-qualified file - the file-granular judgment ephemeral
// classification needs: a replacement file with no executed block was
// never exercised by the probed oracle (never linked, or linked and
// never reached).
func (c Coverage) CoversFile(qualifiedFile string) bool {
	return len(c.covered[qualifiedFile]) > 0
}

// CoverSpanForTest is one literal executed block for CoverageForTest.
type CoverSpanForTest struct {
	StartLine, StartCol, EndLine, EndCol int
}

// CoverageForTest builds a Coverage from literal spans — the
// cross-package seam for bucket-probe tests; production coverage
// comes only from CoveredPositions' profile parse.
func CoverageForTest(spans map[string][]CoverSpanForTest) Coverage {
	covered := map[string][]coverSpan{}
	for file, list := range spans {
		for _, s := range list {
			covered[file] = append(covered[file], coverSpan{startLine: s.StartLine, startCol: s.StartCol, endLine: s.EndLine, endCol: s.EndCol})
		}
	}
	return Coverage{covered: covered}
}
