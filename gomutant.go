// Package gomutant is a mutation tester for Go. It breaks a target symbol's
// body on purpose, runs the tests that vouch for that symbol against each
// mutant, and reports the mutants no test caught (spec overview.md). A
// survivor is a finding: either the test is weak and should be strengthened,
// or the mutant is equivalent and should be dispositioned. gomutant measures
// whether tests have teeth; it never decides whether anything is "covered" —
// that judgment belongs to whatever consumes its findings.
package gomutant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gomutant/internal/engine"
)

// Target is one symbol to mutate, paired with its kill oracle and its labels
// (REQ-target-model): the oracle names the test symbols whose failure counts
// as catching a mutant — empty means the derived default, the tests of the
// symbol's own package (REQ-target-default) — and labels are opaque strings
// echoed unchanged onto every finding the target produces
// (REQ-target-labels).
type Target struct {
	Symbol string   `json:"symbol"`
	Oracle []string `json:"oracle,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// Residue is one changed-but-untargeted path from changed-scope discovery,
// with the engine-level reason it yielded no target (REQ-target-changed).
type Residue struct {
	Path   string
	Reason string
}

// Tree is a loaded Go tree targets resolve against.
type Tree struct {
	eng *engine.Tree
	dir string
}

// Load loads the Go tree rooted at dir: a module, or a workspace whose
// go.work members are all in scope.
func Load(dir string) (*Tree, error) {
	e, err := engine.Load(dir)
	if err != nil {
		return nil, err
	}
	return &Tree{eng: e, dir: dir}, nil
}

// Discover targets every top-level function and method declared in the
// tree's non-test, non-generated source files, oracles left to the default
// (REQ-target-producers): whole-package discovery is a usable run without a
// caller enumerating anything.
func (t *Tree) Discover() []Target {
	syms := t.eng.DeclaredSymbols()
	out := make([]Target, 0, len(syms))
	for _, s := range syms {
		out = append(out, Target{Symbol: s})
	}
	return out
}

// DiscoverChanged targets only the symbols whose bodies differ from a
// reference version (REQ-target-changed): paths are the tree-relative
// changed files, and ref supplies a path's reference content (ok=false for a
// path absent at the reference, so a new file reads as all changed). Beside
// the targets it reports the changed-but-untargeted residue with the
// engine-level reason each path yielded no target, so the caller sees the
// whole changed surface, never a silently narrowed one.
func (t *Tree) DiscoverChanged(paths []string, ref func(path string) ([]byte, bool)) ([]Target, []Residue) {
	var targets []Target
	var residue []Residue
	for _, fs := range t.eng.Surface(paths, ref) {
		switch {
		case fs.IsTest:
			residue = append(residue, Residue{Path: fs.Path, Reason: "test file: tests are oracles, never targets"})
		case !fs.IsGo:
			residue = append(residue, Residue{Path: fs.Path, Reason: "not a Go source file"})
		case !fs.Loaded:
			// The engine cannot see this file's bodies: deleted, unparseable,
			// or excluded by build constraints — an unbound surface, reported
			// as such rather than mislabeled (REQ-target-changed).
			residue = append(residue, Residue{Path: fs.Path, Reason: "not in the loaded packages (deleted, unparseable, or build-excluded)"})
		case fs.Generated:
			residue = append(residue, Residue{Path: fs.Path, Reason: "generated file"})
		case len(fs.Symbols) > 0:
			for _, s := range fs.Symbols {
				targets = append(targets, Target{Symbol: s})
			}
		case fs.DeclaredBodies == 0:
			residue = append(residue, Residue{Path: fs.Path, Reason: "no function body declared"})
		case fs.RefOnlyDecls > 0:
			residue = append(residue, Residue{Path: fs.Path, Reason: "only deleted symbols: nothing remains to mutate"})
		default:
			residue = append(residue, Residue{Path: fs.Path, Reason: "formatting-only churn: every body is canonically unchanged"})
		}
	}
	return targets, residue
}

// targetsDocument is the config-file form of a target set
// (REQ-target-producers): one JSON document, parsed onto the same model as
// every other producer.
type targetsDocument struct {
	Targets []Target `json:"targets"`
}

// ParseTargets parses a JSON target-set document: {"targets": [{"symbol":
// ..., "oracle": [...], "labels": [...]}, ...]}. Every producer reduces to
// this one model (REQ-target-producers).
func ParseTargets(data []byte) ([]Target, error) {
	var doc targetsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("gomutant: parse targets document: %w", err)
	}
	for _, tg := range doc.Targets {
		if tg.Symbol == "" {
			return nil, fmt.Errorf("gomutant: target with no symbol")
		}
	}
	return doc.Targets, nil
}

// resolveOracle returns a target's effective oracle: the explicit test
// symbols when given, else the tests of the symbol's own package
// (REQ-target-oracle, REQ-target-default). A target whose effective oracle
// is empty has nothing that can kill — the caller sees it and decides.
func (t *Tree) resolveOracle(tg Target) []string {
	if len(tg.Oracle) > 0 {
		return tg.Oracle
	}
	pkg, _ := t.eng.PackageOf(tg.Symbol)
	if pkg == "" {
		return nil
	}
	return t.eng.TestsOf(pkg)
}

// pkgRun is one package's oracle execution: the package and the -run
// pattern of exactly its oracle tests. An oracle spanning packages runs per
// package (REQ-exec-oracle-run): one union pattern would also run same-named
// non-oracle tests in sibling packages, whose kills are unattributable.
type pkgRun struct {
	pkg      string
	runRegex string
}

// pkgRuns groups an oracle's test symbols by package into per-package run
// patterns, deterministically ordered.
func pkgRuns(oracle []string) []pkgRun {
	names := map[string][]string{}
	for _, sym := range oracle {
		pkg, fn := splitTestSymbol(sym)
		if pkg == "" || fn == "" {
			continue
		}
		names[pkg] = append(names[pkg], fn)
	}
	pkgs := make([]string, 0, len(names))
	for p := range names {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	out := make([]pkgRun, 0, len(pkgs))
	for _, p := range pkgs {
		fns := names[p]
		sort.Strings(fns)
		out = append(out, pkgRun{pkg: p, runRegex: "^(" + strings.Join(fns, "|") + ")$"})
	}
	return out
}

// splitTestSymbol splits "importpath.TestName" at the last dot: test
// functions are package-scope, so the final segment is always the function.
func splitTestSymbol(symbol string) (pkg, fn string) {
	i := strings.LastIndex(symbol, ".")
	if i < 0 {
		return "", ""
	}
	return symbol[:i], symbol[i+1:]
}

// ParseStipulatorTargets parses stipulator's targets export — the reference
// external producer (REQ-target-producers): each entry's symbol becomes a
// mutated body, its witness tests become the oracle, and its requirement
// identifiers ride as labels. stipulator owns the wire format; this adapter
// owns the mapping, so stipulator stays ignorant of gomutant.
func ParseStipulatorTargets(data []byte) ([]Target, error) {
	var doc struct {
		Version int `json:"stipulatorTargets"`
		Targets []struct {
			Symbol       string   `json:"symbol"`
			Witnesses    []string `json:"witnesses,omitempty"`
			Requirements []string `json:"requirements,omitempty"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("gomutant: parse stipulator targets export: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("gomutant: stipulator targets export version %d not understood (want 1)", doc.Version)
	}
	out := make([]Target, 0, len(doc.Targets))
	for _, t := range doc.Targets {
		if t.Symbol == "" {
			return nil, fmt.Errorf("gomutant: stipulator export entry with no symbol")
		}
		out = append(out, Target{Symbol: t.Symbol, Oracle: t.Witnesses, Labels: t.Requirements})
	}
	return out, nil
}

// LoadTargets parses a targets document of any producer gomutant understands
// (REQ-target-producers), sniffed by its version key: gomutant's own
// document, or stipulator's targets export.
func LoadTargets(data []byte) ([]Target, error) {
	var probe struct {
		Stipulator *int `json:"stipulatorTargets"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("gomutant: parse targets document: %w", err)
	}
	if probe.Stipulator != nil {
		return ParseStipulatorTargets(data)
	}
	return ParseTargets(data)
}
