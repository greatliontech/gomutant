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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/greatliontech/glob"
	"github.com/greatliontech/gofresh/runtimeinput"
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

// FilterTargets selects targets by package import path and fully qualified
// symbol using complete-input glob patterns (REQ-target-filtering). Patterns
// within one kind are alternatives; package and symbol filters both constrain
// the result when supplied.
func (t *Tree) FilterTargets(targets []Target, packagePatterns, symbolPatterns []string) ([]Target, error) {
	return t.FilterTargetsContext(context.Background(), targets, packagePatterns, symbolPatterns)
}

// FilterTargetsContext is FilterTargets with cooperative cancellation.
func (t *Tree) FilterTargetsContext(ctx context.Context, targets []Target, packagePatterns, symbolPatterns []string) ([]Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	packages, err := compile("package", packagePatterns)
	if err != nil {
		return nil, err
	}
	symbols, err := compile("symbol", symbolPatterns)
	if err != nil {
		return nil, err
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
// without running mutants (REQ-target-inspection).
func (t *Tree) DescribeTargets(targets []Target) ([]TargetDescription, error) {
	return t.DescribeTargetsContext(context.Background(), targets)
}

// DescribeTargetsContext is DescribeTargets with cooperative cancellation.
func (t *Tree) DescribeTargetsContext(ctx context.Context, targets []Target) ([]TargetDescription, error) {
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
		sort.Strings(oracle)
		if len(oracle) != 0 {
			if err := t.eng.ValidateOracleContext(ctx, oracle); err != nil {
				return nil, fmt.Errorf("target %s: %w", target.Symbol, err)
			}
		}
		labels := append([]string(nil), target.Labels...)
		sort.Strings(labels)
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
	sort.Slice(descriptions, func(i, j int) bool { return descriptions[i].Symbol < descriptions[j].Symbol })
	return descriptions, ctx.Err()
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
	// vouches is the caller's reviewed dynamic-state vouch set in
	// gofresh's canonical "<import path>.<Variable>" form, installed on
	// every analysis engine the tree constructs so run verdicts,
	// findings inspection, and explain judge under the same
	// acceptances.
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

// DynamicStateVouches reports the installed vouch set — introspection
// for callers auditing which acceptances the tree judges under.
func (t *Tree) DynamicStateVouches() []string {
	return append([]string(nil), t.vouches...)
}

// ParseScratchNamespaces parses DIR:PATTERN scratch-namespace
// declarations (REQ-exec-scratch-namespace): DIR module-relative,
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
// "<import path>:<Variable>" — the colon cannot appear in an import
// path, so the pair is unambiguous and a bare package never parses as
// a vouch — into gofresh's canonical identities, refusing control or
// space characters (unmatchable, and they collide config surfaces) and
// a variable that is not one Go identifier.
func ParseDynamicStateVouches(entries []string) ([]string, error) {
	var identities []string
	seen := map[string]bool{}
	for _, entry := range entries {
		pkg, name, ok := strings.Cut(entry, ":")
		if !ok || pkg == "" || name == "" {
			return nil, fmt.Errorf("gomutant: vouch %q is not <import path>:<Variable>", entry)
		}
		for _, r := range pkg {
			if r <= ' ' || r == 0x7f || unicode.IsControl(r) {
				return nil, fmt.Errorf("gomutant: vouch package %q carries a control or space character", pkg)
			}
		}
		for i, r := range name {
			letter := unicode.IsLetter(r) || r == '_'
			if (i == 0 && !letter) || (i > 0 && !letter && !unicode.IsDigit(r)) {
				return nil, fmt.Errorf("gomutant: vouch variable %q is not one Go identifier", name)
			}
		}
		identity := pkg + "." + name
		if !seen[identity] {
			seen[identity] = true
			identities = append(identities, identity)
		}
	}
	sort.Strings(identities)
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

// CheckToolchainProvenance runs the load-time toolchain guard
// standalone (REQ-exec-provenance) — for verbs that mutate state
// before any tree load would fire it. The directory resolves exactly
// as the load's does, so the two guards name one path.
func CheckToolchainProvenance(ctx context.Context, dir string, sel Selection) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("gomutant: resolve tree root %s: %w", dir, err)
	}
	return engine.CheckToolchainProvenance(ctx, abs, sel)
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
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("gomutant: resolve tree root %s: %w", dir, err)
	}
	e, err := engine.LoadContextSelection(ctx, abs, sel)
	if err != nil {
		return nil, err
	}
	return &Tree{eng: e, dir: abs}, nil
}

// DiscoverContext targets every top-level function and method declared in the
// tree's non-test, non-generated source files — package initializers
// excepted: the language keeps init unreferencable, so it can never be a
// resolvable target — oracles left to the default
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
			residue = append(residue, Residue{Path: fs.Path, Reason: testFileResidueReason})
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
// the run's target set whose records are stale for an oracle-caused
// reason are exactly the measurements this change touched without
// re-measuring, so the row names them and the re-measure move
// (REQ-target-changed). Counting is best-effort - a record whose
// inspection errors is skipped; the run that re-measures it will say
// why - and the rows pass through unchanged when nothing qualifies.
func (t *Tree) OracleClosureSignpostContext(ctx context.Context, residue []Residue, prior []Finding, targets []Target) ([]Residue, error) {
	hasTestRow := false
	for _, r := range residue {
		if r.Reason == testFileResidueReason {
			hasTestRow = true
			break
		}
	}
	if !hasTestRow || len(prior) == 0 {
		return residue, nil
	}
	targeted := make(map[string]bool, len(targets))
	for _, target := range targets {
		targeted[target.Symbol] = true
	}
	var closed []string
	for _, finding := range prior {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if targeted[finding.Symbol] {
			continue
		}
		inspection, err := t.InspectFindingContext(ctx, finding)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
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
	sort.Strings(closed)
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
// symbols when given, else the tests of the symbol's own package
// (REQ-target-oracle, REQ-target-default). A target whose effective oracle
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
	return t.eng.TestsOfContext(ctx, pkg)
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
