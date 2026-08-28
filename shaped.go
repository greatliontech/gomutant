package gomutant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gomutant/internal/engine"
)

// Shaped-target support: structural classes and manual recipes generate
// their candidates from declared parameters instead of the operator
// catalog over a body (REQ-target-structural,
// REQ-target-manual-recipes). A shaped target's Symbol is its
// caller-chosen identity; its pin is the shape digest — the declared
// parameters plus every synthesized replacement (which embeds the
// original content it derives from) — beside the ordinary oracle
// evidence, so a moved input re-measures exactly as a body edit does.

// shapedOperatorSet identifies shaped candidate generation; findings
// pin it exactly as body findings pin OperatorSet, so changing shaped
// generation re-stales every prior shaped record.
const shapedOperatorSet = "shaped/1"

// TargetShape is the recorded declared form of a shaped finding:
// identity and audit (REQ-target-structural,
// REQ-target-manual-recipes).
type TargetShape struct {
	Structural *StructuralSpec `json:"structural,omitempty"`
	Manual     *ManualSpec     `json:"manual,omitempty"`
}

// shapeEqual compares two recorded shapes structurally.
func shapeEqual(a, b *TargetShape) bool {
	ea, erra := json.Marshal(a)
	eb, errb := json.Marshal(b)
	return erra == nil && errb == nil && string(ea) == string(eb)
}

// Shaped reports whether the target declares a shaped form.
func (tg Target) Shaped() bool { return tg.Structural != nil || tg.Manual != nil }

// validateShapedTarget refuses a malformed shaped declaration before
// any resolution: exactly one form, complete class parameters, and an
// explicit oracle — a shaped target has no package to derive tests
// from, and inheriting any would claim vouching nobody stated.
func validateShapedTarget(tg Target) error {
	if tg.Structural != nil && tg.Manual != nil {
		return fmt.Errorf("target %s declares both structural and manual forms", tg.Symbol)
	}
	if len(tg.Oracle) == 0 {
		return fmt.Errorf("target %s: a shaped target requires an explicit oracle", tg.Symbol)
	}
	if s := tg.Structural; s != nil {
		switch s.Class {
		case "import-boundary":
			if len(s.Packages) == 0 || s.Forbidden == "" {
				return fmt.Errorf("target %s: import-boundary requires packages and forbidden", tg.Symbol)
			}
			if s.Type != "" || s.Interface != "" {
				return fmt.Errorf("target %s: import-boundary takes no type/interface parameters", tg.Symbol)
			}
		case "interface-satisfaction":
			if s.Type == "" || s.Interface == "" {
				return fmt.Errorf("target %s: interface-satisfaction requires type and interface", tg.Symbol)
			}
			if len(s.Packages) != 0 || s.Forbidden != "" {
				return fmt.Errorf("target %s: interface-satisfaction takes no packages/forbidden parameters", tg.Symbol)
			}
		default:
			return fmt.Errorf("target %s: unknown structural class %q", tg.Symbol, s.Class)
		}
	}
	if m := tg.Manual; m != nil {
		if m.File == "" || len(m.Edits) == 0 {
			return fmt.Errorf("target %s: a manual recipe requires file and edits", tg.Symbol)
		}
		for i, e := range m.Edits {
			if e.Find == "" {
				return fmt.Errorf("target %s: manual edit %d has an empty find", tg.Symbol, i)
			}
			if e.Find == e.Replace {
				return fmt.Errorf("target %s: manual edit %d replaces find with itself", tg.Symbol, i)
			}
		}
	}
	return nil
}

// shapedCandidates resolves a shaped target to its candidate set and
// shape digest. Candidates carry synthesized whole-file replacements
// through the same overlay every body mutant uses; the digest pins the
// declared parameters and every replacement, so any moved input —
// spec, declaring file, recipe file — re-measures the target
// (REQ-result-stale).
func (t *Tree) shapedCandidates(ctx context.Context, tg Target) ([]engine.Candidate, string, error) {
	var candidates []engine.Candidate
	switch {
	case tg.Structural != nil && tg.Structural.Class == "import-boundary":
		probes, err := t.eng.ImportProbes(ctx, tg.Structural.Packages, tg.Structural.Forbidden)
		if err != nil {
			return nil, "", err
		}
		for _, probe := range probes {
			candidates = append(candidates, engine.Candidate{
				Symbol:       tg.Symbol,
				Operator:     "structural: import-boundary",
				Position:     probe.Package,
				Replacements: []engine.Replacement{{File: probe.File, Source: probe.Source}},
			})
		}
	case tg.Structural != nil:
		probes, err := t.eng.MethodProbes(ctx, tg.Structural.Type, tg.Structural.Interface)
		if err != nil {
			return nil, "", err
		}
		for _, probe := range probes {
			candidates = append(candidates, engine.Candidate{
				Symbol:       tg.Symbol,
				Operator:     "structural: interface-satisfaction",
				Position:     probe.Method,
				Replacements: []engine.Replacement{{File: probe.File, Source: probe.Source}},
			})
		}
	case tg.Manual != nil:
		file := tg.Manual.File
		if filepath.IsAbs(file) || strings.Contains(filepath.ToSlash(file), "../") {
			return nil, "", fmt.Errorf("manual recipe file %s must be a clean tree-relative path", file)
		}
		abs := filepath.Join(t.dir, filepath.FromSlash(file))
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, "", fmt.Errorf("manual recipe file: %w", err)
		}
		edited := string(src)
		for i, e := range tg.Manual.Edits {
			if n := strings.Count(edited, e.Find); n != 1 {
				return nil, "", fmt.Errorf("manual edit %d: find occurs %d times in %s, want exactly once", i, n, file)
			}
			edited = strings.Replace(edited, e.Find, e.Replace, 1)
		}
		candidates = append(candidates, engine.Candidate{
			Symbol:       tg.Symbol,
			Operator:     "manual: recipe",
			Position:     file,
			Replacements: []engine.Replacement{{File: abs, Source: []byte(edited)}},
		})
	}
	digest, err := shapeDigest(tg, candidates)
	if err != nil {
		return nil, "", err
	}
	return candidates, digest, nil
}

// shapeDigest folds the declared shape and every synthesized
// replacement into the target's body-hash pin. Replacements embed the
// original content they derive from (a rewritten declaring file, an
// edited recipe file), so a moved source re-measures without a
// separate content ledger; the import-boundary probe is
// content-independent by design — the oracle's own runtime evidence
// pins what the analyzer read.
func shapeDigest(tg Target, candidates []engine.Candidate) (string, error) {
	h := sha256.New()
	spec := struct {
		Structural *StructuralSpec `json:"structural,omitempty"`
		Manual     *ManualSpec     `json:"manual,omitempty"`
	}{tg.Structural, tg.Manual}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	h.Write(encoded)
	for _, c := range candidates {
		h.Write([]byte{0})
		h.Write([]byte(c.Operator))
		h.Write([]byte{0})
		h.Write([]byte(c.Position))
		for _, r := range c.Replacements {
			h.Write([]byte{0})
			h.Write([]byte(r.File))
			h.Write([]byte{0})
			h.Write(r.Source)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// executeShapedCandidate runs one shaped candidate in a scratch copy of
// the tree. Structural and recipe oracles analyze or read source at
// runtime, so the synthesized state must exist on disk — the build
// overlay reaches only the oracle binary's own compilation — and the
// scratch copy keeps the real tree untouched: break-observe-restore
// with the restore made unnecessary by construction
// (REQ-target-structural, REQ-target-manual-recipes). The run is
// unobserved (runtime manifests would name scratch paths no view
// describes); the finding's evidence is the oracle rows attached to the
// baseline observations, which ran observed on the real tree.
func (t *Tree) executeShapedCandidate(ctx context.Context, w work, m engine.Mutant, opts Options, runEnv []string) (engine.MutantOutcome, string, bool, error) {
	scratch, err := os.MkdirTemp("", "gomutant-shaped-*")
	if err != nil {
		return engine.MutantSurvived, "", false, fmt.Errorf("shaped scratch: %w", err)
	}
	defer os.RemoveAll(scratch)
	if err := copyTreeForShaped(ctx, t.dir, scratch); err != nil {
		return engine.MutantSurvived, "", false, fmt.Errorf("shaped scratch copy: %w", err)
	}
	// The clean scratch twin is the differential base: the mutated
	// scratch carries the probe on disk, so a same-dir baseline would
	// compare the mutant against itself, and a compile refusal could
	// blame the probe for a scratch-infrastructure fault (an
	// out-of-tree replace directive, an escaping symlink) the copy
	// itself broke.
	cleanScratch, err := os.MkdirTemp("", "gomutant-shaped-clean-*")
	if err != nil {
		return engine.MutantSurvived, "", false, fmt.Errorf("shaped scratch: %w", err)
	}
	defer os.RemoveAll(cleanScratch)
	if err := copyTreeForShaped(ctx, t.dir, cleanScratch); err != nil {
		return engine.MutantSurvived, "", false, fmt.Errorf("shaped scratch copy: %w", err)
	}
	for _, r := range m.Replacements {
		rel, err := filepath.Rel(t.dir, r.File)
		if err != nil || strings.HasPrefix(rel, "..") {
			return engine.MutantSurvived, "", false, fmt.Errorf("shaped replacement %s escapes the tree", r.File)
		}
		dst := filepath.Join(scratch, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return engine.MutantSurvived, "", false, err
		}
		if err := os.WriteFile(dst, r.Source, 0o644); err != nil {
			return engine.MutantSurvived, "", false, err
		}
	}
	env := rebaseScratchEnv(runEnv, t.dir, scratch)
	// The twin's environment anchors at the CLEAN copy: in a workspace
	// tree, GOWORK pointing into the mutant scratch would make the
	// "clean" differential build mutant content — the same
	// self-comparison the twin exists to end.
	cleanEnv := rebaseScratchEnv(runEnv, t.dir, cleanScratch)
	// The runner demands a non-empty replacement set (an empty one marks
	// a discarded candidate); the scratch mutant re-asserts the on-disk
	// synthesized files, which is also what makes a compile refusal
	// attributable to the probe.
	scratchMutant := engine.Mutant{Symbol: m.Symbol, Operator: m.Operator, Position: m.Position}
	for _, r := range m.Replacements {
		rel, err := filepath.Rel(t.dir, r.File)
		if err != nil {
			return engine.MutantSurvived, "", false, err
		}
		scratchMutant.Replacements = append(scratchMutant.Replacements, engine.Replacement{File: filepath.Join(scratch, rel), Source: r.Source})
	}
	outcome := engine.MutantSurvived
	killer := ""
	memoryDecided := false
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return outcome, killer, memoryDecided, err
		}
		if outcome != engine.MutantSurvived {
			break
		}
		out, groupKiller, groupMemoryDecided, diagnostic, err := engine.RunMutantBaselineDirEnv(ctx, scratch, cleanScratch, scratchMutant, g.pkgs, g.runRegex, opts.OracleTimeout, g.flags, env, cleanEnv)
		if diagnostic != "" {
			if m.Operator == "structural: interface-satisfaction" {
				// A satisfaction assertion's natural teeth are the
				// compiler's (var _ I = T{}): the broken method set
				// refusing to build IS the oracle catching the probe —
				// but only after the clean twin proves the scratch
				// infrastructure itself builds, or nothing ran and the
				// kill would be fabricated.
				ran, passed, cleanErr := engine.TestProbeEnv(ctx, cleanScratch, g.pkgs[0], g.runRegex, opts.OracleTimeout, g.flags, cleanEnv)
				if cleanErr == nil && ran > 0 && passed {
					return engine.MutantKilled, "compile: " + firstLine(diagnostic), false, nil
				}
				return engine.MutantSurvived, "", false, fmt.Errorf("shaped candidate %s (%s): the clean scratch twin does not pass its oracle (ran=%d passed=%t err=%v), so the compile refusal is a scratch-infrastructure fault, not the probe's: %s", m.Position, m.Operator, ran, passed, cleanErr, diagnostic)
			}
			// Any other shaped candidate failing the oracle's build is
			// a probe fault the caller must see, never a verdict: the
			// compiler text rides the error.
			return engine.MutantSurvived, "", false, fmt.Errorf("shaped candidate %s (%s) does not build in the scratch tree: %s", m.Position, m.Operator, diagnostic)
		}
		if err == nil && out == engine.MutantKilled {
			err = attributedKill(groupKiller, w.oracleSet)
		}
		if err != nil {
			return engine.MutantSurvived, "", false, fmt.Errorf("%s: shaped candidate %s %s: %w", m.Symbol, m.Position, m.Operator, err)
		}
		outcome = out
		killer = groupKiller
		memoryDecided = memoryDecided || groupMemoryDecided
	}
	return outcome, killer, memoryDecided, ctx.Err()
}

// firstLine truncates a compiler diagnostic to its lead line for the
// kill record.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// copyTreeForShaped copies the module tree into the scratch root,
// excluding the VCS bookkeeping tree and the tool's own store — the
// oracle needs the sources, module files, and any fixed data it reads,
// never the repository history.
func copyTreeForShaped(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := d.Name()
		if d.IsDir() && (base == ".git" || base == ".gomutant") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		}
	})
}

// rebaseScratchEnv points tree-anchored environment entries (GOWORK) at
// the scratch copy; everything else — toolchain, caches, the delivered
// width — stays shared, so scratch runs reuse the build cache.
func rebaseScratchEnv(env []string, realRoot, scratch string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "GOWORK") && value != "off" && value != "" {
			if rel, err := filepath.Rel(realRoot, value); err == nil && !strings.HasPrefix(rel, "..") {
				out = append(out, "GOWORK="+filepath.Join(scratch, rel))
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

// shapedProbedFiles names the on-disk files a shaped finding's probes
// derive from, tree-relative: provenance inputs the subject views
// cannot know (REQ-result-layers).
func shapedProbedFiles(dir string, shape *TargetShape) []string {
	if shape == nil {
		return nil
	}
	var files []string
	if shape.Manual != nil {
		// Absolute, matching every other provenance source path: a
		// relative entry fails the root-relative resolution and would
		// force-stamp the finding dirty.
		files = append(files, filepath.Join(dir, filepath.FromSlash(shape.Manual.File)))
	}
	return files
}
