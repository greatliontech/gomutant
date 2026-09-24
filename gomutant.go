// Package gomutant is a mutation tester for Go. It breaks a target symbol's
// body on purpose, runs the tests that vouch for that symbol against each
// mutant, and reports the mutants no test caught (spec overview.md). A
// survivor is a finding: either the test is weak and should be strengthened,
// or the mutant is equivalent and should be dispositioned. gomutant measures
// whether tests have teeth; it never decides whether anything is "covered" —
// that judgment belongs to whatever consumes its findings.
package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/greatliontech/glob"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// Target is one symbol to mutate, paired with its kill oracle and its labels
// (REQ-target-model): the oracle names the test symbols whose failure counts
// as catching a mutant — empty means the derived default, the tests of every
// in-tree package whose test binary links the symbol's package, its own
// included (REQ-target-default) — and labels are opaque strings
// echoed unchanged onto every finding the target produces
// (REQ-target-labels).
type Target struct {
	Symbol string   `json:"symbol"`
	Oracle []string `json:"oracle,omitempty"`
	Labels []string `json:"labels,omitempty"`
	// OracleExplicit marks the oracle as a producer's complete statement of
	// who vouches — even when empty. An explicitly empty oracle derives
	// nothing: the target reports as measurable by nothing rather than
	// inheriting package tests it never claimed (REQ-target-default).
	OracleExplicit bool `json:"oracleExplicit,omitempty"`
	// Structural declares a shaped target whose candidates synthesize a
	// forbidden structural state instead of mutating a body; Symbol is
	// then the target's caller-chosen identity, never a resolvable
	// reference. A structural kill is evidence about the oracle's
	// teeth, never about the soundness of the analyzer behind it
	// (REQ-target-structural).
	Structural *StructuralSpec `json:"structural,omitempty"`
	// Manual declares a recipe-shaped target: caller-authored file
	// edits carrying the target's opaque intent through Labels;
	// targeting stays an input, so identifying the site is the
	// producer's job and the harness owns break-observe-restore
	// (REQ-target-manual-recipes).
	Manual *ManualSpec `json:"manual,omitempty"`
}

// StructuralSpec parameterizes one structural mutation class
// (REQ-target-structural).
type StructuralSpec struct {
	// Class selects the mutation class: "import-boundary" or
	// "interface-satisfaction".
	Class string `json:"class"`
	// Packages scopes an import-boundary probe: one candidate per
	// scoped package, each injecting a blank import of Forbidden.
	Packages []string `json:"packages,omitempty"`
	// Forbidden is the import path an import-boundary oracle must
	// refuse.
	Forbidden string `json:"forbidden,omitempty"`
	// Type and Interface parameterize an interface-satisfaction probe:
	// each candidate breaks one method of Type's satisfaction of
	// Interface, and the oracle must fail.
	Type      string `json:"type,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// ManualEdit is one find/replace edit of a manual recipe; Find must
// occur exactly once in the file, so the edit is position-stable
// without carrying offsets that drift.
type ManualEdit struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// ManualSpec parameterizes one recipe-shaped target: every edit applies
// to File atomically in one candidate (REQ-target-manual-recipes).
type ManualSpec struct {
	File  string       `json:"file"`
	Edits []ManualEdit `json:"edits"`
}

// compileTargetFilters compiles both filter sets with the shared
// teaching error; cancellation keeps per-pattern precedence over a
// compile refusal.
func compileTargetFilters(ctx context.Context, packagePatterns, symbolPatterns []string) (packages, symbols []*glob.Pattern, err error) {
	compile := func(kind string, sources []string) ([]*glob.Pattern, error) {
		patterns := make([]*glob.Pattern, 0, len(sources))
		for _, source := range sources {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pattern, err := glob.Compile(source)
			if err != nil {
				return nil, fmt.Errorf("gomutant: invalid %s filter %q: %w", kind, source, err)
			}
			patterns = append(patterns, pattern)
		}
		return patterns, nil
	}
	if packages, err = compile("package", packagePatterns); err != nil {
		return nil, nil, err
	}
	if symbols, err = compile("symbol", symbolPatterns); err != nil {
		return nil, nil, err
	}
	return packages, symbols, nil
}

// FilterTargets selects targets by package import path and fully qualified
// symbol using complete-input glob patterns (REQ-target-filtering). Patterns
// within one kind are alternatives; package and symbol filters both constrain
// the result when supplied. The context bounds the preparation the
// selection loads.
func (t *Tree) FilterTargets(ctx context.Context, targets []Target, packagePatterns, symbolPatterns []string) ([]Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	packages, symbols, err := compileTargetFilters(ctx, packagePatterns, symbolPatterns)
	if err != nil {
		return nil, err
	}
	// Filters over an already-empty selection are vacuous, never refused:
	// the no-match refusal teaches "fix your patterns", which is false
	// when no pattern could have matched anything — the caller's empty
	// answer names the input that emptied the selection instead
	// (REQ-target-filtering).
	if len(targets) == 0 {
		return nil, nil
	}
	if len(packages) == 0 && len(symbols) == 0 {
		return append([]Target(nil), targets...), nil
	}
	matches := func(patterns []*glob.Pattern, value string) bool {
		if len(patterns) == 0 {
			return true
		}
		for _, pattern := range patterns {
			if pattern.Match(value) {
				return true
			}
		}
		return false
	}
	selected := make([]Target, 0, len(targets))
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !matches(symbols, target.Symbol) {
			continue
		}
		if len(packages) != 0 {
			if target.Shaped() {
				// A shaped identity resolves to no package: a package
				// filter simply never selects it — symbol patterns are
				// the shaped filter surface (REQ-target-filtering).
				continue
			}
			pkgPath, err := t.eng.PackagePathContext(ctx, target.Symbol)
			if err != nil {
				return nil, err
			}
			if !matches(packages, pkgPath) {
				continue
			}
		}
		selected = append(selected, target)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("gomutant: target filters matched no targets (patterns match complete paths; * stays within one slash component and ** as a complete component crosses slash components, for example **/*emitConditions*)")
	}
	return selected, nil
}

// TargetDescription is one target with the effective oracle a run would use.
type TargetDescription struct {
	Symbol         string   `json:"symbol"`
	Oracle         []string `json:"oracle"`
	Labels         []string `json:"labels,omitempty"`
	OracleExplicit bool     `json:"oracleExplicit"`
	// Shaped names the declared shape class of a shaped target
	// ("structural: import-boundary", "structural:
	// interface-satisfaction", "manual: recipe"); empty for symbol
	// targets (REQ-target-inspection).
	Shaped  string `json:"shaped,omitempty"`
	Skipped string `json:"skipped,omitempty"`
}

// DescribeTargets resolves and validates the effective oracle of every target
// without running mutants (REQ-target-inspection), under caller-owned
// cancellation.
func (t *Tree) DescribeTargets(ctx context.Context, targets []Target) ([]TargetDescription, error) {
	descriptions := make([]TargetDescription, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[target.Symbol] {
			return nil, fmt.Errorf("gomutant: duplicate target symbol %s", target.Symbol)
		}
		seen[target.Symbol] = true
		oracle, err := t.resolveOracleContext(ctx, target)
		if err != nil {
			return nil, err
		}
		oracle = append([]string{}, oracle...)
		slices.Sort(oracle)
		if len(oracle) != 0 {
			if err := t.eng.ValidateOracleContext(ctx, oracle); err != nil {
				return nil, fmt.Errorf("target %s: %w", target.Symbol, err)
			}
		}
		labels := append([]string(nil), target.Labels...)
		slices.Sort(labels)
		description := TargetDescription{
			Symbol: target.Symbol, Oracle: oracle, Labels: labels,
			OracleExplicit: target.OracleExplicit || len(target.Oracle) != 0,
		}
		switch {
		case target.Shaped():
			// Inspection cannot disagree with execution: a shaped
			// identity resolves to no body, so description reports the
			// declared class and the same validation refusals the run
			// would issue (REQ-target-structural,
			// REQ-target-manual-recipes).
			switch {
			case target.Structural != nil:
				description.Shaped = "structural: " + target.Structural.Class
			default:
				description.Shaped = "manual: recipe"
			}
			if err := validateShapedTarget(target); err != nil {
				description.Skipped = "shaped target refused: " + err.Error()
			}
		case len(oracle) == 0:
			description.Skipped = "no oracle"
		default:
			_, err := t.eng.BodyHashContext(ctx, target.Symbol)
			if errors.Is(err, engine.ErrNotFunction) {
				description.Skipped = "not a function - for mutation adequacy, target its methods or the bound function-level subjects"
			} else if err != nil {
				return nil, fmt.Errorf("target %s: %w", target.Symbol, err)
			}
		}
		descriptions = append(descriptions, description)
	}
	slices.SortFunc(descriptions, func(a, b TargetDescription) int { return strings.Compare(a.Symbol, b.Symbol) })
	return descriptions, ctx.Err()
}

// Residue is one changed-but-untargeted path from changed-scope discovery,
// with the engine-level reason it yielded no target (REQ-target-changed).
type Residue struct {
	Path   string
	Reason string
	// Package is the import path of a changed test file's package — the
	// package whose oracles its change reaches, a deleted file's
	// derived from its directory — empty for every other row and for a
	// test file no main module holds (REQ-target-changed).
	Package string `json:",omitempty"`
}

// Tree is a loaded Go tree targets resolve against.
type Tree struct {
	eng *engine.Tree
	dir string
	// selection is the run's declared build selection the tree loaded
	// under: the leg a coverage bound names (REQ-result-unreached-bound).
	selection Selection
	// fileVouches is the repository's reviewed standing vouch set: the
	// tree root's `vouches` file read at the load, in gofresh's
	// canonical "<import path>.<Variable>" form.
	fileVouches []string
	// vouches is the invocation's own declarations extending the
	// standing set; the union is installed on every analysis engine the
	// tree constructs (the engines read no file of their own: a
	// workspace member's file is never a second home), so run verdicts,
	// findings inspection, and explain judge under the same acceptances
	// (REQ-exec-preparation).
	vouches []string
}

// SetDynamicStateVouches installs the caller's reviewed dynamic-state
// vouch set — canonical identities from ParseDynamicStateVouches — on
// every analysis engine this tree constructs. The vouches that
// discharged culprits ride each record's subject evidence, so
// acceptance is auditable in the findings document.
// Not synchronized: install before the tree is shared across
// goroutines or calls, never on a live shared tree.
func (t *Tree) SetDynamicStateVouches(identities ...string) {
	t.vouches = append([]string(nil), identities...)
}

// effectiveVouches is the standing file set extended by the installed
// declarations, sorted and deduplicated: the one set every engine
// judges under.
func (t *Tree) effectiveVouches() []string {
	union := append(append([]string(nil), t.fileVouches...), t.vouches...)
	slices.Sort(union)
	return slices.Compact(union)
}

// DynamicStateVouches reports the effective vouch set — the tree
// root's file extended by the installed declarations — introspection
// for callers auditing which acceptances the tree judges under; it is
// never an input to SetDynamicStateVouches, which installs declarations
// alone (the file's set stands on its own).
func (t *Tree) DynamicStateVouches() []string {
	return t.effectiveVouches()
}

// StandingVouches is the repository's reviewed standing vouch set: the
// tree root's `vouches` file in gofresh's grammar — one
// IMPORT-PATH:VARIABLE per line, `#` comments and blank lines ignored,
// an absent file the empty set — read whole or refused (an unreadable
// file, a malformed line). A verb's preparation fires the refusal in
// the root just proven to exist, before the first load; a verb that
// loads with no preparation stage meets it at the load's head, before
// any package loads; the load reads the set (REQ-exec-preparation).
func StandingVouches(root string) ([]string, error) {
	vouches, err := gofresh.ReadVouchFile(filepath.Join(root, gofresh.RepositoryVouchFile))
	if err != nil {
		return nil, fmt.Errorf("gomutant: %w", err)
	}
	return vouches, nil
}

// TreeLoadInput reports whether a file of the given name is one whose
// bytes the tree's load reads directly — the module and workspace
// files, Go sources, and the standing vouch set; non-Go build inputs
// (assembly, cgo, embedded files) are not listed — the one list a cache
// keying a loaded tree must hash, so a missed input never serves a
// stale tree. The name matches anywhere in the tree: a workspace
// member's own vouch file, which the load never reads, still keys the
// cache — an over-invalidation, never a stale serve.
func TreeLoadInput(name string) bool {
	switch name {
	case "go.mod", "go.sum", "go.work", "go.work.sum", "modules.txt", gofresh.RepositoryVouchFile:
		return true
	}
	return strings.HasSuffix(name, ".go")
}

// ParseScratchNamespaces parses DIR:PATTERN scratch-namespace
// declarations (REQ-exec-scratch-namespace): DIR tree-relative,
// PATTERN a single-component os.MkdirTemp-style name pattern. Each
// parsed declaration passes gofresh's namespace grammar here, so a
// malformed one refuses at the boundary - before a measurement whose
// every observation would otherwise degrade at ingest.
func ParseScratchNamespaces(entries []string) ([]runtimeinput.ScratchNamespace, error) {
	var namespaces []runtimeinput.ScratchNamespace
	for _, entry := range entries {
		dir, pattern, ok := strings.Cut(entry, ":")
		if !ok || dir == "" || pattern == "" {
			return nil, fmt.Errorf("gomutant: scratch namespace %q is not DIR:PATTERN", entry)
		}
		if err := runtimeinput.ValidateScratchNamespace(dir, pattern); err != nil {
			return nil, fmt.Errorf("gomutant: scratch namespace %q refused: %w", entry, err)
		}
		namespaces = append(namespaces, runtimeinput.ScratchNamespace{Dir: dir, Pattern: pattern})
	}
	return namespaces, nil
}

// ParseDynamicStateVouches parses caller vouch entries of the form
// IMPORT-PATH:VARIABLE into gofresh's canonical identities —
// each entry by gofresh's own grammar (gofresh.ParseVouchEntry: the
// colon cannot appear in an import path, so the pair is unambiguous
// and a bare package never parses as a vouch; control or space
// characters and a variable that is not one Go identifier refuse) —
// deduplicated and sorted.
func ParseDynamicStateVouches(entries []string) ([]string, error) {
	identities, err := gofresh.ParseVouchEntries(entries)
	if err != nil {
		return nil, fmt.Errorf("gomutant: %w", err)
	}
	return identities, nil
}

// Load loads the Go tree rooted at dir: a module, or a workspace whose
// go.work members are all in scope.
func Load(dir string) (*Tree, error) {
	return LoadContext(context.Background(), dir)
}

// LoadContext is Load with caller-owned cancellation.
func LoadContext(ctx context.Context, dir string) (*Tree, error) {
	return LoadContextSelection(ctx, dir, Selection{})
}

// Selection is a run's declared build selection (build tags and a
// toolchain directive); the zero value selects nothing.
type Selection = engine.Selection

// rootCoordinate is the one coordinate the load and a guard name for a
// tree root: the canonical one where the root resolves
// (gotool.CanonicalDir — two spellings of one directory, a symlinked
// checkout, a `..` through a link, are one coordinate), else the
// spelling made absolute — a fail-safe for the exported guards invoked
// directly, ahead of a caller's own root refusal: every preparation and
// verb in this module refuses the root before asking a guard.
func rootCoordinate(dir string) string {
	if canonical, err := gotool.CanonicalDir(dir); err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// CheckToolchainProvenance runs the load-time toolchain guard
// standalone (REQ-exec-provenance) — for verbs that mutate state
// before any tree load would fire it. The directory resolves exactly
// as the load's does (rootCoordinate), so the guard and the load name
// one path.
func CheckToolchainProvenance(ctx context.Context, dir string, sel Selection) error {
	return engine.CheckToolchainProvenance(ctx, rootCoordinate(dir), sel)
}

// CheckHarnessEnvironment is the load ladder's input-decidable arm —
// the OS environment's own refusals (a package driver, an environment
// the exec form cannot express) and a GODEBUG that silences the
// harness's build-fail events — for a verb's preparation stage, before
// any state persists (REQ-exec-preparation). Its coordinate is the
// load's by construction and unobservable: the arm's inputs are the OS
// environment and the composed GODEBUG, and the composition reads the
// directory only to find its go.work.
func CheckHarnessEnvironment(dir string, sel Selection) error {
	return engine.CheckHarnessEnvironment(rootCoordinate(dir), sel)
}

// LoadContextSelection is LoadContext under a declared build selection:
// the selection rewrites the tree's one frozen environment before
// anything reads it, so package loading, target discovery, constraint
// matching, oracle spawns, and the measurement pins all see the same
// selection by construction. A tag-gated oracle measures exactly as an
// untagged one; the toolchain and build-configuration measurement pins
// carry the selection, so alternating selections re-measure rather
// than serve across each other.
func LoadContextSelection(ctx context.Context, dir string, sel Selection) (*Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One coordinate for the tree root — gofresh's own, the one its
	// engines canonicalize their roots by, so two spellings of one tree
	// (a symlinked checkout, a relative path) are one tree to the
	// evidence root, the machine-local store, and every record; a root
	// that does not resolve is refused as the root, before anything in
	// it is read.
	if err := CheckTreeRoot(dir); err != nil {
		return nil, err
	}
	abs := rootCoordinate(dir)
	// The repository's standing vouch set — one home, the tree root's
	// file — read at the load's head, whole or refused, before any
	// package loads: a verb with no preparation stage pays no load for a
	// refusal decidable here, and no engine reads a file of its own
	// (REQ-exec-preparation).
	fileVouches, err := StandingVouches(abs)
	if err != nil {
		return nil, err
	}
	e, err := engine.LoadContextSelection(ctx, abs, sel)
	if err != nil {
		return nil, err
	}
	return &Tree{eng: e, dir: abs, selection: sel, fileVouches: fileVouches}, nil
}

// Selection is the declared build selection the tree loaded under.
func (t *Tree) Selection() Selection { return t.selection }

// DiscoverContext targets every top-level function and method declared in the
// tree's non-test, non-generated source files, package initializers
// included under their positional identity (`<pkg>.init#<file>#<ordinal>`,
// the language keeping init unreferencable by name) — oracles left to the default
// (REQ-target-producers): whole-package discovery is a usable run without a
// caller enumerating anything.
func (t *Tree) DiscoverContext(ctx context.Context) ([]Target, error) {
	syms, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Target, 0, len(syms))
	for _, s := range syms {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out = append(out, Target{Symbol: s})
	}
	return out, nil
}

// DiscoverChangedContext targets only the symbols whose bodies differ from a
// reference version (REQ-target-changed): paths are the tree-relative
// changed files, and ref supplies a path's reference content (ok=false for a
// path absent at the reference, so a new file reads as all changed). Beside
// the targets it reports the changed-but-untargeted residue with the
// engine-level reason each path yielded no target, so the caller sees the
// whole changed surface, never a silently narrowed one — except paths under
// gomutant's own state directory, which are outside the changed source
// surface entirely (REQ-target-changed).
// A mid-scan tree mutation fails the discovery with the unreadable
// declaration named: the whole changed surface or an error, never a
// silently narrowed surface (REQ-target-changed).
func (t *Tree) DiscoverChangedContext(ctx context.Context, paths []string, ref func(path string) ([]byte, bool)) ([]Target, []Residue, error) {
	var targets []Target
	var residue []Residue
	// The tool's own state directory is outside the changed source
	// surface (REQ-target-changed): its bookkeeping can never produce a
	// mutation target — dot-directories are outside Go package loading,
	// so the targets arm needs no code — and reporting the tool's own
	// writes as residue would be self-noise on every incremental run.
	source := make([]string, 0, len(paths))
	for _, p := range paths {
		if toolOwned(p) {
			continue
		}
		source = append(source, p)
	}
	surface, err := t.eng.SurfaceContext(ctx, source, ref)
	if err != nil {
		return nil, nil, err
	}
	for _, fs := range surface {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		switch {
		case fs.IsTest:
			residue = append(residue, Residue{Path: fs.Path, Reason: testFileResidueReason, Package: fs.Package})
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
	return targets, residue, nil
}

// testFileResidueReason is the changed-scope residue arm for test
// files; the closure signpost anchors on it.
const testFileResidueReason = "test file: tests are oracles, never targets"

// OracleClosureSignpostContext extends changed-scope test-file residue
// rows with what the changed tests closed over: prior findings outside
// the run's target set that the changed test files reach and whose
// records are stale for an oracle-caused reason are exactly the
// measurements this change touched without re-measuring, so the row
// names them and the re-measure move (REQ-target-changed). A changed
// test file reaches, through its package, a record whose recorded
// oracle names a test of that package, and — where the record's
// oracle is derived, never chosen — a record whose own package that
// package's test binary links, the derived oracle's membership rule
// (a test newly written there joins the record's oracle). The reach
// is read from the records and one listing per changed package, never
// judged; only the reached records are judged, so the pass scales
// with what the delta reaches and not with the document. A test file
// with no package (one no main module's package can hold) reaches
// nothing; a package whose listing fails reaches by recorded evidence
// alone.
// Counting is best-effort - a record whose inspection errors is
// skipped; the run that re-measures it will say why - and the rows
// pass through unchanged when nothing qualifies. Each half is priced
// before it is paid: one stage names the changed packages the listings
// cover, another the records, subjects and packages the pass judges.
func (t *Tree) OracleClosureSignpostContext(ctx context.Context, residue []Residue, prior []Finding, targets []Target, progress func(stage string)) ([]Residue, error) {
	changed := map[string]bool{}
	for _, r := range residue {
		if r.Reason == testFileResidueReason && r.Package != "" {
			changed[r.Package] = true
		}
	}
	if len(changed) == 0 || len(prior) == 0 {
		return residue, nil
	}
	// The packages a changed package's test binary links: a derived
	// oracle over any of them includes that package's tests. One
	// listing per changed package, priced before it runs.
	if progress != nil {
		progress(fmt.Sprintf("closure signpost listing the test closure of %d changed package(s)", len(changed)))
	}
	linked := map[string]bool{}
	for _, pkg := range slices.Sorted(maps.Keys(changed)) {
		set, err := t.eng.LinkedTestPackagesContext(ctx, pkg)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		for p := range set {
			linked[p] = true
		}
	}
	targeted := make(map[string]bool, len(targets))
	for _, target := range targets {
		targeted[target.Symbol] = true
	}
	var candidates []Finding
	subjects := map[string]bool{}
	for _, finding := range prior {
		if targeted[finding.Symbol] {
			continue
		}
		// A recorded oracle subject is a test function, whose package
		// the last-dot cut names exactly, a dotted last path element
		// included; a record's own subject may be a Type.Method
		// spelling, which only the loaded packages tell apart from a
		// dotted path element — the string alone guesses short.
		reached := false
		for _, e := range finding.OracleEvidence {
			if pkg, _ := splitTestSymbol(e.Symbol); changed[pkg] {
				reached = true
			}
		}
		if !reached && !finding.OracleExplicit {
			pkg, err := t.eng.PackagePathContext(ctx, finding.Symbol)
			if err != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			reached = err == nil && linked[pkg]
		}
		if !reached {
			continue
		}
		for _, e := range finding.OracleEvidence {
			subjects[e.Symbol] = true
		}
		candidates = append(candidates, finding)
	}
	if len(candidates) == 0 {
		return residue, nil
	}
	// One judged pass over the reached records' shared views, with the
	// per-record boundary: a record whose judgment errors is skipped,
	// the rest still count.
	if progress != nil {
		progress(fmt.Sprintf("closure signpost over %d prior record(s) the changed tests reach (%d subject(s) in %d package(s))", len(candidates), len(subjects), len(changed)))
	}
	inspections, errs, err := t.inspectFindings(ctx, candidates, nil, true)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return residue, nil
	}
	var closed []string
	for i, finding := range candidates {
		if errs[i] != nil {
			continue
		}
		inspection := inspections[i]
		if inspection.State != FindingStale {
			continue
		}
		if !strings.HasPrefix(inspection.Reason, "oracle ") && !strings.HasPrefix(inspection.Reason, "derived oracle") {
			continue
		}
		closed = append(closed, finding.Symbol)
	}
	if len(closed) == 0 {
		return residue, nil
	}
	slices.Sort(closed)
	signpost := fmt.Sprintf("; oracle closure of %d stale finding(s) - re-measure by symbol: %s", len(closed), cappedNameList(closed, "symbols"))
	out := append([]Residue(nil), residue...)
	for i := range out {
		if out[i].Reason == testFileResidueReason {
			out[i].Reason += signpost
		}
	}
	return out, nil
}

// toolOwned reports whether a tree-relative changed path lies in
// gomutant's own state directory.
func toolOwned(p string) bool {
	clean := path.Clean(filepath.ToSlash(p))
	return clean == ".gomutant" || strings.HasPrefix(clean, ".gomutant/")
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
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("gomutant: parse targets document: invalid UTF-8")
	}
	topKnown := map[string]bool{"targets": true}
	fields, err := decodeKnownObject(data, topKnown)
	if err != nil {
		return nil, fmt.Errorf("gomutant: parse targets document: %w", err)
	}
	if err := rejectUnknownObjectFields(data, topKnown); err != nil {
		return nil, fmt.Errorf("gomutant: parse targets document: %w", err)
	}
	if targets, ok := fields["targets"]; !ok || isJSONNull(targets) {
		return nil, fmt.Errorf("gomutant: parse targets document: missing field targets")
	}
	var raw struct {
		Targets []json.RawMessage `json:"targets"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("gomutant: parse targets document: %w", err)
	}
	doc := targetsDocument{Targets: make([]Target, len(raw.Targets))}
	known := map[string]bool{"symbol": true, "oracle": true, "labels": true, "oracleExplicit": true, "structural": true, "manual": true}
	for i, entry := range raw.Targets {
		entryFields, err := decodeKnownObject(entry, known)
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse target %d: %w", i, err)
		}
		if err := rejectUnknownObjectFields(entry, known); err != nil {
			return nil, fmt.Errorf("gomutant: parse target %d: %w", i, err)
		}
		symbol, ok := entryFields["symbol"]
		if !ok || isJSONNull(symbol) {
			return nil, fmt.Errorf("gomutant: target %d with no symbol", i)
		}
		for _, name := range []string{"oracle", "labels", "oracleExplicit"} {
			if value, ok := entryFields[name]; ok && isJSONNull(value) {
				return nil, fmt.Errorf("gomutant: target %d field %s is null", i, name)
			}
		}
		for _, name := range []string{"oracle", "labels"} {
			value, ok := entryFields[name]
			if !ok {
				continue
			}
			var elements []json.RawMessage
			if err := json.Unmarshal(value, &elements); err != nil {
				return nil, fmt.Errorf("gomutant: parse target %d field %s: %w", i, name, err)
			}
			for j, element := range elements {
				if isJSONNull(element) {
					return nil, fmt.Errorf("gomutant: target %d field %s element %d is null", i, name, j)
				}
			}
		}
		entryDec := json.NewDecoder(bytes.NewReader(entry))
		entryDec.DisallowUnknownFields()
		if err := entryDec.Decode(&doc.Targets[i]); err != nil {
			return nil, fmt.Errorf("gomutant: parse target %d: %w", i, err)
		}
	}
	for _, tg := range doc.Targets {
		if tg.Symbol == "" {
			return nil, fmt.Errorf("gomutant: target with no symbol")
		}
	}
	return doc.Targets, nil
}

func rejectUnknownObjectFields(data []byte, known map[string]bool) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("object key is not a string")
		}
		if !known[name] {
			return fmt.Errorf("unknown field %s", name)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	return nil
}

// resolveOracle returns a target's effective oracle: the explicit test
// symbols when the target declares them — an explicit inventory, an
// explicitly empty one included, overrides the derivation whole — else
// the derived one: the runnable tests of every in-tree package whose
// test binary links the symbol's package, the symbol's own included,
// so a CLI or app test that links the package is a legitimate oracle
// and the evidence its observation bracket captures is a legitimate
// record (REQ-target-oracle, REQ-target-default). A target whose effective oracle
// is empty has nothing that can kill — the caller sees it and decides.
func (t *Tree) resolveOracle(tg Target) []string {
	oracle, _ := t.resolveOracleContext(context.Background(), tg)
	return oracle
}

func (t *Tree) resolveOracleContext(ctx context.Context, tg Target) ([]string, error) {
	if len(tg.Oracle) > 0 || tg.OracleExplicit {
		return tg.Oracle, ctx.Err()
	}
	pkg, _, err := t.eng.PackageOfContext(ctx, tg.Symbol)
	if err != nil {
		return nil, err
	}
	if pkg == "" {
		return nil, nil
	}
	// The derived oracle spans every in-tree package whose test binary
	// links the symbol's package (REQ-target-default); the derivation is
	// freshness-verified and memoized in the engine, and the describe
	// face reports the oracle alone — the run names any stood-down
	// packages on its decisions.
	oracle, _, err := t.eng.DerivedOracleContext(ctx, pkg)
	return oracle, err
}

// pkgRun is one package's oracle execution: the package and the -run
// pattern of exactly its oracle tests. An oracle spanning packages runs per
// package (REQ-exec-oracle-run): one union pattern would also run same-named
// non-oracle tests in sibling packages, whose kills are unattributable.
type pkgRun struct {
	pkg      string
	runRegex string
}

// testRunRegex is the ONE builder of a -run pattern over exact test
// function names — pkgRuns and the execution schedule both speak it,
// so a scheduled subset can never drift from the oracle's own pattern
// grammar.
func testRunRegex(fns []string) string {
	return "^(" + strings.Join(fns, "|") + ")$"
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
	slices.Sort(pkgs)
	out := make([]pkgRun, 0, len(pkgs))
	for _, p := range pkgs {
		fns := names[p]
		slices.Sort(fns)
		out = append(out, pkgRun{pkg: p, runRegex: testRunRegex(fns)})
	}
	return out
}

// splitTestSymbol splits "importpath.TestName" at the last dot. Its
// INPUT-CLASS CONTRACT: callers feed package-scope TEST-FUNCTION
// symbols exclusively (killers past the attribution filter, oracle
// members, oracle evidence symbols), where the final segment is
// always the function — so the last-dot cut is exact even for a
// dotted package path element, the case symbolPackage's
// first-dot-after-slash grammar must guess at. The two cutters share
// one symbol grammar but different input classes: symbolPackage cuts
// ARBITRARY subject symbols (methods included) and owns the dotted
// path ambiguity; this cutter's exactness is bought by its narrower
// class, and a method-valued input would mis-split here — feed those
// to symbolPackage.
func splitTestSymbol(symbol string) (pkg, fn string) {
	i := strings.LastIndex(symbol, ".")
	if i <= strings.LastIndex(symbol, "/") {
		// No dot in the name position: a dot inside the path (or none
		// at all) is not a name cut — refuse rather than mint a
		// slash-carrying function name.
		return "", ""
	}
	return symbol[:i], symbol[i+1:]
}

// LoadTargets parses a targets document of any producer gomutant understands
// (REQ-target-producers).
func LoadTargets(data []byte) ([]Target, error) {
	return ParseTargets(data)
}

// LoadTargetsContext is LoadTargets with cancellation before and after decoding.
func LoadTargetsContext(ctx context.Context, data []byte) ([]Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targets, err := LoadTargets(data)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}
