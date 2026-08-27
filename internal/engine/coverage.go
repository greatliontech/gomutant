package engine

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"maps"
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
	// unsound marks import-path-qualified files whose coverage the
	// directive seam refused to re-key (contention, collision): no
	// query about them is a coverage verdict.
	unsound map[string]bool
}

// coverSpan is one executed coverage block's extent.
type coverSpan struct {
	startLine, startCol, endLine, endCol int
}

// DirectiveCoverageView is the directive seam's product: the profile
// re-keying map and the on-disk files whose coverage is KNOWN-UNSOUND.
// cmd/cover keys a //line file's blocks under the directive's filename
// while numbering lines by the on-disk file (the hybrid was measured,
// not assumed), and every other position in this engine is on-disk —
// so profile ingest normalizes the one foreign coordinate at its seam.
// A rename registers only when its on-disk side is a compiled,
// non-test source of the base package (cgo intermediates' //line
// back-references point AT real sources and register nothing) and the
// directive name is neither a sibling cover instruments in the base
// namespace (non-external, non-test — a name cover binds nothing to
// cannot be stolen from) nor claimed by more than one on-disk file.
// Refusals are representable: every claimant of a contended key and
// both sides of a collision land in Unsound, and no query about an
// unsound file is a coverage verdict — the buckets stay empty
// (REQ-exec-survivor-evidence's fail-closed posture).
type DirectiveCoverageView struct {
	Renames map[string]string
	Unsound map[string]bool
}

// DirectiveCoverage builds the seam's view once per Tree — a pure
// function of the loaded syntax and line tables.
func (t *Tree) DirectiveCoverage() DirectiveCoverageView {
	t.directiveOnce.Do(func() {
		// Guard sets are per BASE package path. The VALUE side unions
		// every variant's compiled files (external _test packages
		// included; redundant with the test-file clause below by
		// language rule, kept as namespace alignment). The KEY side
		// holds only what cover instruments under the base namespace
		// — see the sibling-set comment below.
		compiled := map[string]map[string]bool{}
		siblingBase := map[string]map[string]bool{}
		for _, pkg := range t.pkgs {
			bp := basePackagePath(pkg)
			if compiled[bp] == nil {
				compiled[bp] = map[string]bool{}
				siblingBase[bp] = map[string]bool{}
			}
			for _, gf := range pkg.GoFiles {
				clean := filepath.Clean(gf)
				compiled[bp][clean] = true
				// The sibling set holds only names cover actually
				// instruments in the base namespace: non-external
				// (external variants are instrumented under their own
				// import path) AND non-test (in-package _test.go
				// files are never instrumented under any invocation
				// gomutant makes). A name cover binds nothing to
				// cannot be stolen from — treating it as a sibling
				// refuses correct renames and manufactures unsound
				// marks for covered carriers.
				if base := filepath.Base(clean); pkg.PkgPath == bp && !strings.HasSuffix(base, "_test.go") {
					siblingBase[bp][base] = true
				}
			}
		}
		candidates := map[string]map[string]bool{}
		unsound := map[string]bool{}
		for _, pkg := range t.pkgs {
			bp := basePackagePath(pkg)
			for _, f := range pkg.Syntax {
				onDiskAbs := filepath.Clean(pkg.Fset.PositionFor(f.Pos(), false).Filename)
				if !compiled[bp][onDiskAbs] {
					continue
				}
				onDisk := filepath.Base(onDiskAbs)
				if strings.HasSuffix(onDisk, "_test.go") {
					// Coverage consumers only ever query target and
					// replacement files; a test file as a rename's
					// on-disk side is pure key-theft risk, never a
					// join anyone needs.
					continue
				}
				seen := map[string]bool{}
				ast.Inspect(f, func(n ast.Node) bool {
					if n == nil || !n.Pos().IsValid() {
						return true
					}
					// The one sanctioned adjusted read in the engine:
					// this IS the translation seam, enumerating the
					// directive spellings over the node positions —
					// exactly the domain cover keys blocks from, so a
					// block-form directive ahead of same-line code is
					// seen where a per-line walk missed it.
					adjusted := pkg.Fset.Position(n.Pos()).Filename
					if adjusted == "" || seen[adjusted] {
						return true
					}
					seen[adjusted] = true
					base := filepath.Base(adjusted)
					if base == onDisk {
						return true
					}
					if siblingBase[bp][base] {
						// Collision: the carrier's blocks live under
						// the sibling's key and pollute it with
						// on-disk-numbered lines — both sides lose
						// their coverage verdict.
						unsound[bp+"/"+onDisk] = true
						unsound[bp+"/"+base] = true
						return true
					}
					key := bp + "/" + base
					if candidates[key] == nil {
						candidates[key] = map[string]bool{}
					}
					candidates[key][bp+"/"+onDisk] = true
					return true
				})
			}
		}
		renames := map[string]string{}
		for key, values := range candidates {
			if len(values) != 1 {
				// Two on-disk files claiming one directive name (the
				// generated-from-one-grammar shape): cover merged both
				// files' blocks under the key, so handing the merge to
				// either steals from the other and pollutes the
				// winner. Every claimant loses its coverage verdict.
				for value := range values {
					unsound[value] = true
				}
				continue
			}
			for value := range values {
				renames[key] = value
			}
		}
		t.directiveView = DirectiveCoverageView{Renames: renames, Unsound: unsound}
	})
	return t.directiveView
}

// CoveredPositions runs the oracle's baseline once with coverage
// instrumentation over the target's package and reports the positions
// its tests reach. The profile is measured on the unmutated tree, so
// bucketing a survivor with it is advisory classification, never a
// measurement pin (REQ-exec-survivor-evidence). view re-keys
// //line-directive profile entries to their on-disk spelling and
// carries the known-unsound files into the result
// (DirectiveCoverage), so coverage joins speak the engine's one
// coordinate system and refusals stay representable.
func CoveredPositions(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, binFlags, env []string, view DirectiveCoverageView) (Coverage, error) {
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
		name := p.FileName
		if onDisk, ok := view.Renames[name]; ok {
			name = onDisk
		}
		for _, b := range p.Blocks {
			if b.Count == 0 {
				continue
			}
			covered[name] = append(covered[name], coverSpan{startLine: b.StartLine, startCol: b.StartCol, endLine: b.EndLine, endCol: b.EndCol})
		}
	}
	return Coverage{covered: covered, unsound: maps.Clone(view.Unsound)}, nil
}

// Merge unions another profile's executed extents into this one.
func (c Coverage) Merge(other Coverage) Coverage {
	if c.covered == nil {
		c.covered = make(map[string][]coverSpan)
	}
	for file, spans := range other.covered {
		c.covered[file] = append(c.covered[file], spans...)
	}
	if len(other.unsound) > 0 && c.unsound == nil {
		c.unsound = map[string]bool{}
	}
	for file := range other.unsound {
		c.unsound[file] = true
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

// Unsound reports whether the file's coverage was refused at the
// directive seam: no Covered/Intersects/CoversFile answer about it is
// a coverage verdict, and consumers must leave their buckets empty.
func (c Coverage) Unsound(qualifiedFile string) bool {
	return c.unsound[qualifiedFile]
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

// UnsoundForTest marks files refused at the directive seam — the
// cross-package seam for bucket tests pinning the empty-bucket
// posture.
func (c Coverage) UnsoundForTest(files ...string) Coverage {
	if c.unsound == nil {
		c.unsound = map[string]bool{}
	}
	for _, f := range files {
		c.unsound[f] = true
	}
	return c
}
