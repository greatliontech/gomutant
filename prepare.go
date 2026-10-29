package gomutant

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// CampaignInputs is what a findings-producing run knows at its flag
// parse: the inputs every refusal below is decidable from, before the
// tree loads.
type CampaignInputs struct {
	// Selection is the run's declared build selection: the harness
	// environment it composes is judged here, before the lock.
	Selection Selection
	// FindingsPath is the findings document the campaign holds.
	FindingsPath string
	// ModuleDir is the tree root the store's layer judgment resolves
	// against.
	ModuleDir string
	// Plan is the preflight form: it takes no campaign lock, since it
	// persists nothing and must not mint the lock file a killed run
	// leaves behind.
	Plan bool
	// Budget, OracleTimeout, and AnalysisBudget are the run's bounds,
	// sign-checked here.
	Budget         int
	OracleTimeout  time.Duration
	AnalysisBudget time.Duration
	// ScratchNamespaces, Vouches, and BracketPaths are the caller's
	// declarations, parsed here so a malformed one refuses before any
	// load; a bracket path is also proven present and capturable under
	// the tree root here, the base every spawn's bracket resolves
	// against.
	ScratchNamespaces []string
	Vouches           []string
	BracketPaths      []string
	// TargetSources names the target sources the caller supplied, in
	// the caller's own spelling — the flags or parameters given; at most
	// one may be given. Targets carries the one given, read here at its
	// enumerated place; Packages and Symbols are the filters, and
	// CutChanged asks a changed-ref selection for its survivor cut (a
	// plan renders none).
	TargetSources []string
	Targets       TargetInputs
	Packages      []string
	Symbols       []string
	CutChanged    bool
}

// CampaignPreparation is the prepared campaign: every declaration
// parsed, the lock held, the exemptions and the store open — the
// material a run needs before its first load, produced by
// PrepareCampaign.
type CampaignPreparation struct {
	ScratchNamespaces []runtimeinput.ScratchNamespace
	Vouches           []string
	Exemptions        []Exemption
	Store             *Store
	// Prior is the findings document as read at preparation — the
	// document's own refusals (unreadable, a version this binary does
	// not read) fire here.
	Prior []Finding
	// Request is the target-source request the dispatch resolves after
	// the load: the document parsed and the ref's surface read here.
	Request SelectionRequest
	// ReleaseCampaign releases the campaign lock; a no-op for a plan.
	ReleaseCampaign func()
}

// validateRunBounds refuses a negative budget, oracle timeout, or
// analysis budget — the one check the preparation and the library entry
// (Tree.Run) share.
func validateRunBounds(budget int, oracleTimeout, analysisBudget time.Duration) error {
	if budget < 0 {
		return fmt.Errorf("gomutant: budget must be non-negative")
	}
	if analysisBudget < 0 {
		return fmt.Errorf("gomutant: analysis budget must be non-negative")
	}
	if oracleTimeout < 0 {
		return fmt.Errorf("gomutant: oracle timeout must be non-negative")
	}
	return nil
}

// TargetSource is one of a face's target-source knobs in the face's
// own spelling, and whether the call gave it.
type TargetSource struct {
	Name  string
	Given bool
}

// TargetSourcesGiven names the sources a call gave, in the face's
// order — the one preamble both faces hand ValidateTargetSources.
func TargetSourcesGiven(sources ...TargetSource) []string {
	var given []string
	for _, s := range sources {
		if s.Given {
			given = append(given, s.Name)
		}
	}
	return given
}

// ValidateTargetSources refuses more than one supplied target source,
// naming the ones given in the caller's own spelling.
func ValidateTargetSources(given []string) error {
	if len(given) > 1 {
		return fmt.Errorf("give one target source: %s were given - at most one", strings.Join(given, " and "))
	}
	return nil
}

// PrepareCampaign fires every refusal a findings-producing run can
// decide from its inputs alone, in one place and before any tree load:
// the bounds' signs, the target-source exclusivity and the targets
// document's parse, the scratch and vouch declarations, the
// selection's shape, the tree root's existence and then the changed
// ref's surface (the git seam reads in that root), the harness
// environment (the load ladder's input-decidable arm), the bracket
// paths' shape and presence, the exemptions document, the findings
// document (unreadable, or a version this binary does not read), and
// last the campaign lock (fail-fast — a
// second campaign against the same document refuses immediately,
// naming the holder, per REQ-exec-exclusivity) — last, because the
// lock's file outlives its holder by design, so no refusal may follow
// it. A run that fails any of them pays nothing (REQ-exec-preparation).
func PrepareCampaign(ctx context.Context, in CampaignInputs) (*CampaignPreparation, error) {
	if err := validateRunBounds(in.Budget, in.OracleTimeout, in.AnalysisBudget); err != nil {
		return nil, err
	}
	if err := ValidateTargetSources(in.TargetSources); err != nil {
		return nil, err
	}
	request := SelectionRequest{Packages: in.Packages, Symbols: in.Symbols, Cut: in.CutChanged}
	targets, given, err := in.Targets.parseTargets(ctx)
	if err != nil {
		return nil, err
	}
	request.Targets, request.TargetsGiven = targets, given
	scratch, err := ParseScratchNamespaces(in.ScratchNamespaces)
	if err != nil {
		return nil, err
	}
	var vouches []string
	if len(in.Vouches) > 0 {
		if vouches, err = ParseDynamicStateVouches(in.Vouches); err != nil {
			return nil, err
		}
	}
	if err := in.Selection.Validate(); err != nil {
		return nil, err
	}
	// The tree root is an input too: a root that is not a directory
	// refuses here, before the lock — whose directory creation would
	// otherwise conjure an empty tree for the load to find.
	if err := CheckTreeRoot(in.ModuleDir); err != nil {
		return nil, err
	}
	// The standing vouch set's shape, read from the root just proven to
	// exist: refused here, before the lock and the load; the load reads
	// the set (REQ-exec-preparation).
	if _, err := StandingVouches(in.ModuleDir); err != nil {
		return nil, err
	}
	// The ref's surface is read in the root just proven to exist.
	if request.Changed, err = in.Targets.readChanged(ctx); err != nil {
		return nil, err
	}
	// The load ladder's environment arm is decidable from the OS
	// environment, the root, and the selection alone — no process
	// spawned: the ambient refusals (a package driver, an environment
	// the exec form cannot express) and a GODEBUG that silences the
	// harness's build-fail events refuse here, before the lock, and
	// again at the load's head by construction (REQ-exec-provenance).
	// The GODEBUG arm reads the selection-applied environment the load
	// reads — the term the spec names: no selection sets GODEBUG; the
	// selection's own shape refused above, with the declarations.
	if err := CheckHarnessEnvironment(in.ModuleDir, in.Selection); err != nil {
		return nil, err
	}
	// A bracket path's shape and presence are decidable from the tree
	// root alone (REQ-exec-observation) — refused here, before the lock
	// and the load; whether the bracket can fingerprint it is the run's
	// once-per-run capture at the loaded-set stage.
	if err := validateBracketPaths(in.ModuleDir, in.BracketPaths); err != nil {
		return nil, err
	}
	if err := bracketPathsExist(ctx, in.ModuleDir, in.BracketPaths); err != nil {
		return nil, err
	}
	// The store opens the exemptions record beside the document; the
	// document itself is read now, so its refusals fire before any load.
	store, err := OpenStore(in.FindingsPath, in.ModuleDir)
	if err != nil {
		return nil, err
	}
	prior, err := store.Load(ctx)
	if err != nil {
		return nil, err
	}
	release := func() {}
	if !in.Plan {
		if release, err = AcquireCampaignLock(in.FindingsPath); err != nil {
			return nil, err
		}
	}
	return &CampaignPreparation{ScratchNamespaces: scratch, Vouches: vouches, Exemptions: store.Exemptions(), Store: store, Prior: prior, Request: request, ReleaseCampaign: release}, nil
}

// ValidateEphemeralRuns refuses a runs count outside 1..MaxEphemeralRuns
// (0 reads as 1) — decidable at flag parse, so it fires before any load.
func ValidateEphemeralRuns(runs int) error {
	if runs < 0 || runs > MaxEphemeralRuns {
		return fmt.Errorf("runs %d is outside 1-%d (omitted means 1): each run is a full oracle process", runs, MaxEphemeralRuns)
	}
	return nil
}

// ValidateAttestationReason refuses an attestation whose reasoning is
// blank: the record carries the reasoning, so a probe that would end in
// an unrecordable attestation is refused before it runs.
func ValidateAttestationReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("gomutant: an equivalence attestation needs its reasoning on the record")
	}
	return nil
}

// PrepareAttestation runs the pre-write preparation checks shared by
// the CLI and MCP survivor-attestation faces: tree root existence,
// standing vouch set validation, and toolchain provenance verification.
func PrepareAttestation(ctx context.Context, dir string, sel Selection) error {
	if err := CheckTreeRoot(dir); err != nil {
		return err
	}
	if _, err := StandingVouches(dir); err != nil {
		return err
	}
	return CheckToolchainProvenance(ctx, dir, sel)
}

// AttestedLoadSelection resolves the selection under which a finding
// record is loaded on attest or explain: an explicit selection wins,
// and an unscoped call falls back to the record's own selection key.
func AttestedLoadSelection(sel Selection, hasSelection bool, attested Finding) Selection {
	if !hasSelection && attested.Selection != "" {
		return SelectionOfKey(attested.Selection)
	}
	return sel
}

// ValidateRetargetPair refuses a malformed rename pair on the two
// strings alone — the predicates need no tree, so they fire before any
// load (REQ-result-lifecycle's pair shape).
func ValidateRetargetPair(from, to string) error {
	if from == "" || to == "" {
		return fmt.Errorf("retarget needs a non-empty from and to prefix")
	}
	if from == to {
		return fmt.Errorf("retarget needs distinct prefixes - from and to are both %q", from)
	}
	// The observation-subject halves rewrite under independent
	// projections of the pair; a structurally asymmetric pair (one half
	// package-shaped, the other symbol-shaped) would mangle the local
	// half into an identity that names nothing, durably.
	fromPkg, fromLocal := retargetPrefixParts(from)
	toPkg, toLocal := retargetPrefixParts(to)
	if (fromLocal == "") != (toLocal == "") {
		return fmt.Errorf("retarget prefixes must be structurally alike - %q and %q split package and symbol differently; use a package pair or a full-symbol pair", from, to)
	}
	// A symbol pair renames within its package: the destination carries
	// no stored fact to validate a package move against, and a dotted
	// destination remainder may continue a package instead of naming a
	// local - so the package halves must agree and the local halves map
	// segment for segment.
	if fromLocal != "" {
		if fromPkg != toPkg {
			return fmt.Errorf("retarget: a symbol pair renames within its package - %q and %q name different packages; move a surface across packages with a package pair", from, to)
		}
		if strings.Count(fromLocal, ".") != strings.Count(toLocal, ".") {
			return fmt.Errorf("retarget: %q -> %q restructures the local name - a rename maps segments one to one, and a dotted remainder may continue a package instead of naming a local; a package move takes a package pair, and a promotion or demotion that reshapes the name re-measures under the new shape", from, to)
		}
	}
	// Unlike-terminated pairs splice across unlike edges: with from
	// "example.com/old." and to "example.com/new", the matched dot is
	// consumed and never re-emitted, writing example.com/newTestF
	// durably. The terminator is part of the claim - both prefixes
	// carry the same one, or neither.
	terminator := func(p string) byte {
		if c := p[len(p)-1]; c == '.' || c == '/' {
			return c
		}
		return 0
	}
	if terminator(from) != terminator(to) {
		return fmt.Errorf("retarget prefixes must be like-terminated - %q and %q end differently, and splicing across unlike edges corrupts identities; terminate both with the same separator or neither", from, to)
	}
	return nil
}

// CheckTreeRoot refuses a tree root that is not a directory — the
// refusal every preparation fires before reading anything in the root
// (REQ-exec-preparation); a verb wiring its own preparation, and the
// load's own head, read it first too.
func CheckTreeRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("gomutant: tree root %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("gomutant: tree root %s is not a directory", root)
	}
	return nil
}

// group is one test-binary invocation: a package, its oracle's -run
// pattern, and the binary's flags.
type group struct {
	pkgs                  []string
	passed                []string
	runRegex              string
	flags                 []string
	moduleDir, packageDir string
}

type packageContextResult struct {
	moduleDir, packageDir string
	err                   error
}

type oracleValidationResult struct {
	err error
}

// derivedOracleMemo is one package's memoized expanded derivation:
// the oracle and the packages whose closures stood down.
type derivedOracleMemo struct {
	oracle    []string
	stoodDown []string
}

type runPreparation struct {
	packageOf func(context.Context, string) (string, string, error)
	// deriveOracle is the expanded, freshness-verified default-oracle
	// derivation (REQ-target-default): (oracle, stood-down packages,
	// error). One implementation lives in the engine; the seam exists
	// for preparation unit tests.
	deriveOracle func(context.Context, string) ([]string, []string, error)
	scopeOracle  func(string, string, []string) []string
	validate     func(context.Context, []string) error
	contextFor   func(context.Context, string) (string, string, error)
	runtimesOf   func(context.Context, []string) (map[string][]string, error)

	derivedOracles map[string]derivedOracleMemo
	validations    map[string]oracleValidationResult
	contexts       map[string]packageContextResult
	rapid          map[string]bool
	runtimes       map[string][]string
	notedProperty  map[string]bool
}

func newRunPreparation(t *Tree) *runPreparation {
	return &runPreparation{
		packageOf:      t.eng.PackageOfContext,
		deriveOracle:   t.eng.DerivedOracleContext,
		scopeOracle:    t.eng.ScopeDerivedOracle,
		validate:       t.eng.ValidateOracleContext,
		contextFor:     t.eng.PackageContextContext,
		runtimesOf:     t.eng.PropertyRuntimesContext,
		derivedOracles: map[string]derivedOracleMemo{},
		validations:    map[string]oracleValidationResult{},
		contexts:       map[string]packageContextResult{},
	}
}

// oracle resolves a target's oracle set: the producer's explicit
// statement, or the derivation over the target's package. derived
// reports that the derivation RAN over a resolved package — an explicit
// (even empty) statement and a symbol in no loaded package are not
// derivations, so an empty set from them is never the selection's
// coverage bound (REQ-result-unreached-bound).
func (p *runPreparation) oracle(ctx context.Context, target Target) (oracle, stoodDown []string, derived bool, err error) {
	if len(target.Oracle) > 0 || target.OracleExplicit {
		return slices.Clone(target.Oracle), nil, false, ctx.Err()
	}
	pkg, _, err := p.packageOf(ctx, target.Symbol)
	if err != nil {
		return nil, nil, false, err
	}
	if pkg == "" {
		return nil, nil, false, nil
	}
	if memo, ok := p.derivedOracles[pkg]; ok {
		oracle = slices.Clone(memo.oracle)
		stoodDown = slices.Clone(memo.stoodDown)
	} else {
		oracle, stoodDown, err = p.deriveOracle(ctx, pkg)
		if err != nil {
			return nil, nil, false, err
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		p.derivedOracles[pkg] = derivedOracleMemo{oracle: slices.Clone(oracle), stoodDown: slices.Clone(stoodDown)}
	}
	if p.scopeOracle != nil {
		oracle = p.scopeOracle(pkg, target.Symbol, oracle)
	}
	return oracle, stoodDown, true, ctx.Err()
}

func (p *runPreparation) validateOracle(ctx context.Context, oracle []string) error {
	key := sequenceKey(oracle)
	if result, ok := p.validations[key]; ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		return result.err
	}
	err := p.validate(ctx, oracle)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return cancelErr
	}
	p.validations[key] = oracleValidationResult{err: err}
	return err
}

func (p *runPreparation) packageContext(ctx context.Context, pkg string) (string, string, error) {
	if result, ok := p.contexts[pkg]; ok {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		return result.moduleDir, result.packageDir, result.err
	}
	moduleDir, packageDir, err := p.contextFor(ctx, pkg)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return "", "", cancelErr
	}
	p.contexts[pkg] = packageContextResult{moduleDir: moduleDir, packageDir: packageDir, err: err}
	return moduleDir, packageDir, err
}

// rapidPackages derives the rapid set from the one runtime-detection
// walk (propertyRuntimes): the split, the flags, the statements, and
// the regime pin all consume a single derivation and can never
// disagree.
// candidates feeds the shared propertyRuntimes memo: whichever caller
// derives first fixes the memo's package list, so every caller must
// pass the same run-wide oracle set (both in-tree callers pass
// oraclePackages).
func (p *runPreparation) rapidPackages(ctx context.Context, candidates []string) (map[string]bool, error) {
	if p.rapid != nil {
		return p.rapid, ctx.Err()
	}
	runtimes, err := p.propertyRuntimes(ctx, candidates)
	if err != nil {
		return nil, err
	}
	p.rapid = map[string]bool{}
	for pkg, names := range runtimes {
		if slices.Contains(names, "rapid") {
			p.rapid[pkg] = true
		}
	}
	return p.rapid, nil
}

// noteProperty reports whether pkg's prerequisite statement has not yet
// been emitted this run and marks it emitted - one statement per
// package per run.
func (p *runPreparation) noteProperty(pkg string) bool {
	if p.notedProperty == nil {
		p.notedProperty = map[string]bool{}
	}
	if p.notedProperty[pkg] {
		return false
	}
	p.notedProperty[pkg] = true
	return true
}

// propertyRuntimes memoizes the oracle packages' recognized property
// runtimes for the run's prerequisite statements
// (REQ-exec-property-oracles).
func (p *runPreparation) propertyRuntimes(ctx context.Context, candidates []string) (map[string][]string, error) {
	if p.runtimes != nil {
		return p.runtimes, ctx.Err()
	}
	runtimes, err := p.runtimesOf(ctx, candidates)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.runtimes = runtimes
	if p.runtimes == nil {
		p.runtimes = map[string][]string{}
	}
	return p.runtimes, nil
}

func sequenceKey(values []string) string {
	var key strings.Builder
	for _, value := range values {
		fmt.Fprintf(&key, "%d:", len(value))
		key.WriteString(value)
	}
	return key.String()
}

// probeOracleInstability attributes unverifiable runtime evidence under
// a package-derived oracle by probing each oracle test alone: a test
// whose solo baseline run produces unverifiable evidence is the
// instability's producer; a clean sweep means no single test reproduces
// it (REQ-exec-oracle-guidance). Probes are best-effort - a probe that
// errors, matches nothing, or fails skips its test rather than
// aborting the run whose finding already committed.
func (t *Tree) probeOracleInstability(ctx context.Context, oracle []string, groups []group, opts runOptions, runEnv []string) (oracleAttribution, error) {
	byPkg := make(map[string]group, len(groups))
	for _, g := range groups {
		byPkg[g.pkgs[0]] = g
	}
	var attr oracleAttribution
	for _, test := range oracle {
		if err := ctx.Err(); err != nil {
			return oracleAttribution{}, err
		}
		pkg, fn := splitTestSymbol(test)
		g, ok := byPkg[pkg]
		if pkg == "" || fn == "" || !ok {
			continue
		}
		_, passed, _, _, observed, err := seams.advisoryProbe(ctx, t.dir, pkg, "^"+regexp.QuoteMeta(fn)+"$", opts.advisoryLeash(g), g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
		if err != nil {
			if ctx.Err() != nil {
				return oracleAttribution{}, ctx.Err()
			}
			if attr.firstErr == "" {
				attr.firstErr = err.Error()
			}
			continue
		}
		attr.completed++
		if state, serr := runtimeinput.CompletedState(observed); serr == nil {
			if paths, pok := moduleRelInputs(state.Manifest, t.dir); pok {
				for _, rel := range paths {
					if attr.probedPaths == nil {
						attr.probedPaths = map[string]bool{}
					}
					attr.probedPaths[rel] = true
				}
			}
		}
		if passed && observed.Unverifiable {
			attr.unstable = append(attr.unstable, test)
			// The test's own solo observation attributes its own read,
			// named with that and never with the union's, in the
			// portable form the record's rows carry (the process's
			// directory tree-relative) so the guidance names the same
			// directory the row does; a conversion that fails leaves
			// the observation's own form, still the test's own.
			solo := observed
			if portable, perr := runtimeinput.Relative(observed, t.dir, runEnv); perr == nil {
				solo = portable
			}
			soloReason := ""
			if state, serr := runtimeinput.CompletedState(solo); serr == nil {
				soloReason = state.Reason
			}
			if attribution := evidenceAttribution(solo.Attribution, soloReason); attribution != "" {
				if attr.attributions == nil {
					attr.attributions = map[string]string{}
				}
				attr.attributions[test] = attribution
			}
		}
	}
	return attr, nil
}

// contradictKilledDispositions reports each prior disposition whose
// mutant the current outcomes killed — evidence beats attestation, the
// killer named from the durable keystone (REQ-attest-survivor). A
// disposition whose mutant still survives, or is absent from the kill
// ledger (vanished, de-selected), is left to the carry and merge layers.
// A served record whose kill ledger is incomplete (predating kill
// attribution) is deliberately in the second class: with no ledger, a
// killed disposition cannot be told from a vanished one, and an
// attribution-less contradiction would misfire on the vanished — the
// merge layer's loud no-longer-reported shed covers both honestly.
func contradictKilledDispositions(symbol string, prior []Attestation, survivors []Survivor, kills []Kill, emit func(AttestationContradiction)) {
	if emit == nil || len(prior) == 0 {
		return
	}
	surviving := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		surviving[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	killerOf := make(map[survivorKey]string, len(kills))
	for _, kill := range kills {
		killerOf[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	for _, attestation := range prior {
		key := survivorKey{attestation.Position, attestation.Operator}
		if surviving[key] {
			continue
		}
		if killer, killed := killerOf[key]; killed {
			emit(AttestationContradiction{
				Symbol: symbol, Position: attestation.Position,
				Operator: attestation.Operator, Killer: killer, Reason: attestation.Reason,
			})
		}
	}
}

// AttestationCarry reports one disposition carried across moved
// measurement pins under a held mutation domain (REQ-attest-survivor).
type AttestationCarry struct {
	Symbol   string
	Position string
	Operator string
	Reason   string
	// Derivation names a closure-derivation move between the prior
	// record and the current measurement ("<prior> -> <current>"),
	// empty when the derivation held or the prior record predates the
	// field: when named, the moved pins are the closure hashes the new
	// derivation folded, not a source move.
	Derivation string
}

// Text is the carry's one rendering on every face; a derivation move
// is named, so the reader knows the pins moved under a new closure
// derivation rather than a source edit.
func (c AttestationCarry) Text() string {
	text := c.Symbol + " " + c.Position + " " + c.Operator + " - measurement pins moved; the mutated source is unchanged and the mutant survived re-execution"
	if c.Derivation != "" {
		text += " (closure derivation " + c.Derivation + ")"
	}
	return text
}

// derivationMove names a closure-derivation change between a prior
// record and the current measurement ("<prior> -> <current>"), or
// nothing when it held — or when the prior record predates the field:
// an unrecorded derivation is unknown, never a move.
func derivationMove(prior, current SubjectEvidence) string {
	if prior.ClosureStrategy == "" || prior.ClosureStrategy == current.ClosureStrategy {
		return ""
	}
	return prior.ClosureStrategy + " -> " + current.ClosureStrategy
}

// AttestationContradiction reports one attested survivor a fresh measurement
// killed, with the shed attestation's reasoning and
// the killer so the re-judgment starts from the evidence.
type AttestationContradiction struct {
	Symbol   string
	Position string
	Operator string
	Killer   string
	Reason   string
}

// Text is the contradiction's one rendering after its symbol: the
// attested survivor, its killer, and the reasoning the shed strips.
func (c AttestationContradiction) Text() string {
	return "attested survivor " + c.Position + " (" + c.Operator + ") killed by " + c.Killer + "; attestation shed (was: " + c.Reason + ")"
}

// validateBracketPaths refuses declarations the observation bracket
// cannot honor, loudly and before any measurement. A relative path is
// tree-relative — resolved against the invocation's tree root, one
// declared surface for every module's oracles — so one that escapes
// the tree names nothing the bracket can cover (declare it absolute),
// and one under a tool-excluded directory would be silently uncovered.
// An absolute path, file or directory, declares an external surface
// as it is: a replace module outside the repository is one surface,
// walked by the bracket, never an enumeration of its files
// (REQ-exec-observation).
func validateBracketPaths(treeDir string, paths []string) error {
	for _, p := range paths {
		if filepath.IsAbs(p) {
			// The in-tree absolute spelling is the relative declaration:
			// judged as one, so a tool-excluded path refuses under either
			// spelling instead of recording excluded and uncovered. The
			// tree root is judged in both its given and its resolved form
			// (moduleRelInputs keeps the same pair): a repository reached
			// through a symlinked prefix spells its own files either way.
			rel, ok := treeRelative(treeDir, p)
			if !ok {
				continue
			}
			p = rel
		}
		clean := path.Clean(filepath.ToSlash(p))
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("gomutant: bracket path %s escapes the tree root; a relative path resolves against the tree, declare a surface outside it absolute", p)
		}
		for _, excluded := range []string{".git", ".stipulator", ".gomutant"} {
			if clean == excluded || strings.HasPrefix(clean, excluded+"/") {
				return fmt.Errorf("gomutant: bracket path %s lies under tool-excluded %s and would be silently uncovered", p, excluded)
			}
		}
	}
	return nil
}

// bracketPathsExist proves each declared bracket path exists under the
// tree root - the base every spawn's frame resolves against - the
// input-decidable half of the preflight, fired before the lock and the
// load on both faces (REQ-exec-preparation): the field failure this
// guards against declared a transient per-test directory that test
// cleanup deleted before end-bracket hashing. A surface the oracle
// reads exists before the run; a path that legitimately churns wants a
// scratch namespace, not a bracket path.
func bracketPathsExist(ctx context.Context, treeDir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(treeDir)
	if err != nil {
		return fmt.Errorf("gomutant: bracket path preflight: %w", err)
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := p
		if !filepath.IsAbs(p) {
			full = filepath.Join(root, filepath.FromSlash(p))
		}
		if _, err := os.Stat(full); err != nil {
			return fmt.Errorf("gomutant: bracket path %s does not exist at run start (%v); a transient per-test path wants --scratch-namespace, not a bracket path", p, err)
		}
	}
	return nil
}

// preflightBracketPaths is the loaded-set half of the preflight, once
// per run before the first spawn: the declared paths exist, and a
// bracket captured under the tree root with the spawn's own option set
// fingerprints every one of them — an unhashable object, a root under
// or containing a volatile OS root, the filesystem root refuse here
// instead of sealing every observation of a burned campaign
// (REQ-exec-observation).
func preflightBracketPaths(ctx context.Context, treeDir string, paths []string) error {
	if err := bracketPathsExist(ctx, treeDir, paths); err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	root, err := filepath.EvalSymlinks(treeDir)
	if err != nil {
		return fmt.Errorf("gomutant: bracket path preflight: %w", err)
	}
	if err := engine.PreflightBracket(ctx, root, paths); err != nil {
		return fmt.Errorf("gomutant: bracket path preflight refused (%v); refusing before measurement rather than sealing every observation", err)
	}
	return nil
}

func snapshotTargets(targets []Target) []Target {
	snapshot := slices.Clone(targets)
	for i := range snapshot {
		snapshot[i].Oracle = slices.Clone(snapshot[i].Oracle)
		snapshot[i].Labels = slices.Clone(snapshot[i].Labels)
	}
	return snapshot
}

func snapshotFindings(findings []Finding) []Finding {
	snapshot := slices.Clone(findings)
	for i := range snapshot {
		snapshot[i] = cloneFinding(snapshot[i])
	}
	return snapshot
}

func reportPreparation(callback func(PreparationEvent), event PreparationEvent) {
	if callback != nil {
		callback(event)
	}
}

// explicitOracle reports the target's oracle mode: a stated list, or an
// explicitly empty statement, is the caller's oracle; neither is a
// derivation (REQ-result-stale pins the mode on both sides of a serve).
func (tg Target) explicitOracle() bool { return tg.OracleExplicit || len(tg.Oracle) != 0 }

// orderOracleGroups orders a target's required oracle groups by RTA static
// reachability priority (groups with at least one test reaching the target
// symbol precede groups with no reaching tests) and ascending measured
// baseline duration, without narrowing the required oracle set
// (REQ-exec-oracle-run).
func orderOracleGroups(
	w *work,
	targetSymbol, targetPkg string,
	reaches func(fromSymbol, targetSymbol string) bool,
	groupStats func(g group) (raw time.Duration, testDurations map[string]time.Duration),
) {
	if w == nil || w.shaped || len(w.groups) <= 1 {
		return
	}
	type rankedGroup struct {
		group    group
		baseline runtimeinput.Observation
		hasBase  bool
		pkg      string
		reaches  bool
		effDur   time.Duration
		raw      time.Duration
		index    int
	}
	ranked := make([]rankedGroup, len(w.groups))
	for i, g := range w.groups {
		pkg := ""
		if len(g.pkgs) > 0 {
			pkg = g.pkgs[0]
		}
		var raw time.Duration
		var testDurations map[string]time.Duration
		if groupStats != nil {
			raw, testDurations = groupStats(g)
		}
		rtaReaches := false
		var sumTestDur time.Duration
		tests := g.passed
		if len(tests) == 0 {
			for sym := range w.oracleSet {
				if p, _ := splitTestSymbol(sym); p == pkg {
					tests = append(tests, sym)
				}
			}
		}
		for _, testSym := range tests {
			if p, _ := splitTestSymbol(testSym); p != pkg {
				continue
			}
			if w.oracleSet != nil && !w.oracleSet[testSym] {
				continue
			}
			if reaches == nil || targetSymbol == "" || reaches(testSym, targetSymbol) {
				rtaReaches = true
			}
			if d := testDurations[testSym]; d > 0 {
				sumTestDur += d
			}
		}
		if len(tests) == 0 {
			rtaReaches = pkg == targetPkg || reaches == nil || targetSymbol == ""
		}
		effDur := sumTestDur
		if effDur <= 0 {
			effDur = raw
		}
		rg := rankedGroup{
			group:   g,
			pkg:     pkg,
			reaches: rtaReaches,
			effDur:  effDur,
			raw:     raw,
			index:   i,
		}
		if i < len(w.baselines) {
			rg.baseline = w.baselines[i]
			rg.hasBase = true
		}
		ranked[i] = rg
	}
	slices.SortStableFunc(ranked, func(a, b rankedGroup) int {
		if a.reaches != b.reaches {
			if a.reaches {
				return -1
			}
			return 1
		}
		if a.effDur != b.effDur {
			if a.effDur < b.effDur {
				return -1
			}
			return 1
		}
		if a.raw != b.raw {
			if a.raw < b.raw {
				return -1
			}
			return 1
		}
		aOwn := a.pkg == targetPkg
		bOwn := b.pkg == targetPkg
		if aOwn != bOwn {
			if aOwn {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.pkg, b.pkg); c != 0 {
			return c
		}
		if c := strings.Compare(a.group.runRegex, b.group.runRegex); c != 0 {
			return c
		}
		if a.index < b.index {
			return -1
		}
		if a.index > b.index {
			return 1
		}
		return 0
	})
	for i, rg := range ranked {
		w.groups[i] = rg.group
		if rg.hasBase && i < len(w.baselines) {
			w.baselines[i] = rg.baseline
		}
	}
}
