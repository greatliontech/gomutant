package gomutant

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// FindingState classifies whether a persisted finding still addresses the
// current tree. It is advisory inspection, not a mutation result.
type FindingState string

const (
	FindingCurrent      FindingState = "current"
	FindingStale        FindingState = "stale"
	FindingUnverifiable FindingState = "unverifiable"
	FindingDetached     FindingState = "detached"
	// FindingRecorded is the inspection-without-judgment presentation
	// state: the record's facts as persisted, with no freshness
	// derivation against the current tree. Never a judgment class —
	// a caller filtering by judged state must opt into judging.
	FindingRecorded FindingState = "recorded"
)

// FindingInspection is one finding's current applicability and reason.
// CandidateEvidence carries the record's candidate-local unverifiability: the
// record itself stays classifiable by its subject evidence while each flagged
// candidate reports its own incomplete-process reason
// (REQ-result-inspection; candidate evidence, REQ-result-record).
type FindingInspection struct {
	State             FindingState        `json:"state"`
	Reason            string              `json:"reason,omitempty"`
	CandidateEvidence []CandidateEvidence `json:"candidateEvidence,omitempty"`
}

// RecordedInspection is the tree-free inspection view: the record's
// own facts under the presentation state FindingRecorded, no
// freshness derived. Every inspection surface builds its recorded
// default from this one constructor so the surfaces cannot drift.
func RecordedInspection(finding Finding) FindingInspection {
	return FindingInspection{State: FindingRecorded, CandidateEvidence: append([]CandidateEvidence(nil), finding.CandidateEvidence...)}
}

type subjectView struct {
	symbol    string
	subject   gofresh.Subject
	moduleDir string
	// evidenceDir is the tree root every subject's runtime-input
	// evidence is anchored at — the base its manifest is recorded
	// relative to and re-hashed under — so a workspace member's record
	// names a root-module input tree-relative, portable in the committed
	// document, and the store's portable-line walk resolves it at the
	// tree (REQ-result-layers). The persisted module base is absent
	// for these records; a record from before the anchor carries its
	// member base and the walk honors it.
	evidenceDir string
	env         []string
	view        *gofresh.View
	fp          gofresh.Fingerprint
	sourceFiles []string
	module      *moduleSubjectView
}

type moduleSubjectView struct {
	view     *gofresh.View
	validate func(context.Context) error
	producer bool
}

type subjectViewSet struct {
	bySymbol map[string]*subjectView
	modules  []*moduleSubjectView
}

// subjectEngines shares one gofresh engine per module-directory configuration
// across every view one run constructs. Engine construction validates the
// build configuration against the tree, so constructing one engine per view
// repeats that work once per target; the tree's process environment is fixed,
// which makes the module directory the whole configuration key. Views are
// still constructed per call: a producer view's capture-attach-validate
// transaction is per subject set and cannot be shared across targets.
type subjectEngines struct {
	env []string
	// evidenceEnv is the environment oracle evidence digests under -
	// env with the injected inner-parallelism width
	// (engine.OracleEvidenceEnv). It is the engines' declared producer
	// env and every subject view's revalidation env; env alone keeps
	// serving loads and analysis. Captured at construction, so the
	// width install must precede newSubjectEngines.
	evidenceEnv []string
	vouches     []string
	event       func(phase, pkg, detail string)
	// packageProcess carries the package-process attestation
	// (gofresh WithPackageProcessExecution) for the engines this set
	// builds, fixed at construction: gomutant runs every oracle as
	// `go test` of the oracle packages, and the processes that
	// ATTRIBUTE a subject's verdicts are exactly its own target's
	// oracle-package binaries — so the attestation is honest per
	// target when that target's oracle packages equal its own, and a
	// mixed run builds one engine set per mode rather than flipping a
	// shared flag.
	packageProcess bool
	// treeDir is the evidence root every engine declares: a subject's
	// runtime-input evidence is anchored at the tree, not its module
	// (subjectView.evidenceDir), so the engine's own revalidation
	// re-hashes under the same base the record was made under.
	treeDir string
	byDir   map[string]*gofresh.Engine
}

func (t *Tree) newSubjectEngines(event func(phase, pkg, detail string), packageProcess bool) *subjectEngines {
	env := t.eng.GoEnv()
	return &subjectEngines{env: env, evidenceEnv: engine.OracleEvidenceEnv(env), vouches: t.vouches, event: event, packageProcess: packageProcess, treeDir: t.dir, byDir: map[string]*gofresh.Engine{}}
}

func (e *subjectEngines) engineFor(dir string) (*gofresh.Engine, error) {
	if engine, ok := e.byDir[dir]; ok {
		return engine, nil
	}
	opts := []gofresh.Option{gofresh.WithDir(dir), gofresh.WithEnv(e.env...), gofresh.WithProducerEnv(e.evidenceEnv...), gofresh.WithEvidenceRoot(e.treeDir)}
	if e.packageProcess {
		opts = append(opts, gofresh.WithPackageProcessExecution())
	}
	if len(e.vouches) > 0 {
		opts = append(opts, gofresh.WithDynamicStateVouches(e.vouches...))
	}
	if event := e.event; event != nil {
		// One channel end to end, mirroring gofresh's Progress: the
		// keep-alive/diagnostic split (throttle the former, never the
		// latter) is the consumer's, keyed on detail emptiness — a
		// routing layer here would reintroduce a droppable leg.
		opts = append(opts, gofresh.WithProgress(func(p gofresh.Progress) {
			event(p.Phase, p.Package, p.Detail)
		}))
	}
	engine, err := gofresh.New(opts...)
	if err != nil {
		return nil, err
	}
	e.byDir[dir] = engine
	return engine, nil
}

// symbolPackage cuts a subject symbol's package path: the first dot
// after the last slash bounds the path — a plain function's name and a
// method's Type.Method spelling alike follow it — except a version
// path element (the gopkg.in pattern, exactly ".vN"), which belongs to
// the path: without the absorption "gopkg.in/yaml.v3.Marshal" grouped
// as "gopkg.in/yaml" and a genuinely dark versioned package merged
// with its sibling (the chunk-132 review's L2). A dotted path element
// outside the vN pattern stays ambiguous against a Type.Method
// spelling and keeps the first-dot cut — NOT benign everywhere: two
// dotted sibling package dirs truncate to one prefix, and
// packageProcessAttestable would then grant process-execution honesty
// across them; the string alone cannot decide that edge, the loaded
// package set can. splitTestSymbol is this grammar's sibling for the
// NARROWER test-function input class, where the last-dot cut is exact
// — the two cutters trade generality for exactness and neither
// subsumes the other.
func symbolPackage(symbol string) string {
	slash := strings.LastIndex(symbol, "/")
	rest := symbol[slash+1:]
	offset := slash + 1
	for {
		dot := strings.Index(rest, ".")
		if dot < 0 {
			return symbol
		}
		segment := rest[dot+1:]
		end := strings.Index(segment, ".")
		if end < 0 {
			end = len(segment)
		}
		if v := segment[:end]; len(v) >= 2 && v[0] == 'v' && allDigits(v[1:]) {
			offset += dot + 1 + end
			rest = segment[end:]
			continue
		}
		return symbol[:offset+dot]
	}
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// packageProcessAttestable reports whether a target's measurement
// processes are its own package's test binary: every oracle symbol's
// package equals the target's (gofresh WithPackageProcessExecution's
// honesty condition — gomutant runs oracles as `go test` of the oracle
// packages).
func packageProcessAttestable(targetSymbol string, oracle []string) bool {
	targetPkg := symbolPackage(targetSymbol)
	for _, symbol := range oracle {
		if symbolPackage(symbol) != targetPkg {
			return false
		}
	}
	return true
}

// findingPackageProcessAttestable is packageProcessAttestable over a
// record's evidence rows.
func findingPackageProcessAttestable(f Finding) bool {
	oracle := make([]string, 0, len(f.OracleEvidence))
	for _, evidence := range f.OracleEvidence {
		oracle = append(oracle, evidence.Symbol)
	}
	return packageProcessAttestable(f.Symbol, oracle)
}

// subjectViewBuildHook observes each subject-view build with its
// requested symbols — a test seam for the one-build claims (the batched
// judge's one set per posture, the campaign's one build per mode shared
// by the decision and the producer roles). Fired from the one build
// loop over resolved groups, so every build is counted whatever its
// caller's fault disposition; a strict call aborting at resolution
// built nothing and fires nothing. Sequential tests only, as every
// package-level seam.
var subjectViewBuildHook func(symbols []string)

// observedUnionHook observes each proof capture pass with the symbols
// it covers, fired before the first module's capture — a test seam for
// the capture-time fault routes (a tree moving between a strict
// build's construction and its proof capture). Sequential tests only.
var observedUnionHook func(symbols []string)

func (t *Tree) newSubjectViews(ctx context.Context, symbols []string, packageProcess bool) (*subjectViewSet, error) {
	return t.newStrictSubjectViews(ctx, symbols, t.eng.PackageContextContext, t.newSubjectEngines(nil, packageProcess))
}

// buildSubjectViews is the ONE view-set build: symbols resolve and
// group by module directory, each group gets one gofresh view (one
// observation), each subject its base fingerprint; a symbol that fails
// to resolve, or a module group whose engine, view, or capture fails,
// records the fault for each affected symbol instead of aborting the
// set — target-local evidence faults (REQ-exec-quiescence); only the
// run's own cancellation aborts. The decision set is this set; the
// observed union captures its proofs on these same views (observed);
// the strict callers promote the faults (newSubjectViewsWithPackageContext).
func (t *Tree) buildSubjectViews(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), engines *subjectEngines) (*subjectViewSet, map[string]error, error) {
	faults := map[string]error{}
	groups, err := t.resolveModuleGroups(ctx, symbols, packageContext, func(symbol string, err error) error {
		faults[symbol] = err
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	set, err := t.buildGroupViews(ctx, symbols, groups, engines, faults)
	if err != nil {
		return nil, nil, err
	}
	return set, faults, nil
}

// buildGroupViews is the build half of the one loop: one gofresh view
// per resolved module group (splintered per package on failure), each
// subject's base fingerprint; a group or capture fault records into
// faults for each affected symbol. The resolution half is the caller's,
// with its own fault policy (tolerant: record; strict: abort before any
// view is built — no wasted construction behind a resolution fault).
// requested is the symbol list groups was resolved from — the build
// hook's argument (the requested set, unresolvable symbols included),
// never read for the build itself.
func (t *Tree) buildGroupViews(ctx context.Context, requested []string, groups []moduleGroup, engines *subjectEngines, faults map[string]error) (*subjectViewSet, error) {
	if subjectViewBuildHook != nil {
		subjectViewBuildHook(requested)
	}
	set := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(requested))}
	env := engines.evidenceEnv
	capture := func(ctx context.Context, view *gofresh.View, module *moduleSubjectView, resolved []resolvedSubject) error {
		for _, r := range resolved {
			if err := ctx.Err(); err != nil {
				return err
			}
			fp, err := view.Capture(ctx, r.subject)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				faults[r.symbol] = err
				continue
			}
			sourceFiles, err := view.SourceFilesFor(r.subject)
			if err != nil {
				faults[r.symbol] = err
				continue
			}
			set.bySymbol[r.symbol] = &subjectView{
				symbol: r.symbol, subject: r.subject, moduleDir: r.moduleDir, evidenceDir: t.dir,
				env: env, view: view, fp: fp, sourceFiles: sourceFiles, module: module,
			}
		}
		return nil
	}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		groupEngine, err := engines.engineFor(group.dir)
		if err != nil {
			for _, resolved := range group.resolved {
				faults[resolved.symbol] = err
			}
			continue
		}
		view, err := groupEngine.NewViewFor(ctx, group.subjects, group.dir, gofresh.CodeResult)
		if err == nil {
			module := &moduleSubjectView{view: view, validate: view.Validate}
			set.modules = append(set.modules, module)
			if err := capture(ctx, view, module, group.resolved); err != nil {
				return nil, err
			}
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Splinter retry: the module group batches every subject into
		// one view, so one broken package would fault its healthy
		// siblings wholesale - regroup per package and build each
		// subset alone, and only subjects whose own closure carries the
		// breakage fault (REQ-exec-quiescence). The batch view stays
		// the healthy-path cost; the splinter runs only on failure.
		byPkg := map[string][]resolvedSubject{}
		var order []string
		for _, resolved := range group.resolved {
			if _, ok := byPkg[resolved.subject.Package]; !ok {
				order = append(order, resolved.subject.Package)
			}
			byPkg[resolved.subject.Package] = append(byPkg[resolved.subject.Package], resolved)
		}
		for _, pkg := range order {
			subset := byPkg[pkg]
			subjects := make([]gofresh.Subject, 0, len(subset))
			for _, r := range subset {
				subjects = append(subjects, r.subject)
			}
			subView, subErr := groupEngine.NewViewFor(ctx, subjects, group.dir, gofresh.CodeResult)
			if subErr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				for _, r := range subset {
					faults[r.symbol] = subErr
				}
				continue
			}
			subModule := &moduleSubjectView{view: subView, validate: subView.Validate}
			set.modules = append(set.modules, subModule)
			if err := capture(ctx, subView, subModule, subset); err != nil {
				return nil, err
			}
		}
	}
	return set, nil
}

// resolvedSubject and moduleGroup carry symbol resolution grouped by
// module directory — the shared front half of every view-set build.
type resolvedSubject struct {
	symbol, moduleDir string
	subject           gofresh.Subject
}

type moduleGroup struct {
	dir      string
	resolved []resolvedSubject
	subjects []gofresh.Subject
}

// resolveModuleGroups resolves symbols and groups them by module
// directory. Fault routing is the caller's: fault returning nil records
// the symbol's failure and drops it from the grouping (the union's
// per-symbol tolerance); returning the error aborts the resolution (the
// strict build). Only the context's own cancellation aborts otherwise.
func (t *Tree) resolveModuleGroups(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), fault func(symbol string, err error) error) ([]moduleGroup, error) {
	groups := make([]moduleGroup, 0)
	groupByDir := map[string]int{}
	seen := map[string]bool{}
	for _, symbol := range symbols {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		pkg, local, err := t.eng.PackageOfContext(ctx, symbol)
		if err != nil {
			if err := fault(symbol, err); err != nil {
				return nil, err
			}
			continue
		}
		if pkg == "" || local == "" {
			if err := fault(symbol, fmt.Errorf("subject %s does not resolve", symbol)); err != nil {
				return nil, err
			}
			continue
		}
		moduleDir, _, err := packageContext(ctx, pkg)
		if err != nil {
			if err := fault(symbol, err); err != nil {
				return nil, err
			}
			continue
		}
		resolved := resolvedSubject{symbol: symbol, moduleDir: moduleDir, subject: gofresh.Subject{Package: pkg, Symbol: local}}
		index, ok := groupByDir[moduleDir]
		if !ok {
			index = len(groups)
			groupByDir[moduleDir] = index
			groups = append(groups, moduleGroup{dir: moduleDir})
		}
		groups[index].resolved = append(groups[index].resolved, resolved)
		groups[index].subjects = append(groups[index].subjects, resolved.subject)
	}
	return groups, nil
}

// newStrictSubjectViews is the strict build: a resolution fault aborts
// before any view is constructed, and a build fault is the error — the
// first in the caller's symbol order, so a caller learns its own
// symbol's failure after the splinter narrowed it rather than the
// group's.
func (t *Tree) newStrictSubjectViews(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), engines *subjectEngines) (*subjectViewSet, error) {
	groups, err := t.resolveModuleGroups(ctx, symbols, packageContext, func(_ string, err error) error { return err })
	if err != nil {
		return nil, err
	}
	faults := map[string]error{}
	set, err := t.buildGroupViews(ctx, symbols, groups, engines, faults)
	if err != nil {
		return nil, err
	}
	if err := firstFault(symbols, faults); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

// newStrictObservedViews is the strict build with the observation
// proofs captured: the per-target rebuild's shape, returning the
// observed set so a narrowing over uncaptured proofs stays
// unrepresentable on that path too.
func (t *Tree) newStrictObservedViews(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), engines *subjectEngines) (*observedViewSet, error) {
	set, err := t.newStrictSubjectViews(ctx, symbols, packageContext, engines)
	if err != nil {
		return nil, err
	}
	union, faults, err := set.observed(ctx)
	if err != nil {
		return nil, err
	}
	if err := firstFault(symbols, faults); err != nil {
		return nil, err
	}
	return union, nil
}

// firstFault promotes a fault map to one error: the first faulting
// symbol in the caller's order.
func firstFault(symbols []string, faults map[string]error) error {
	for _, symbol := range symbols {
		if fault, ok := faults[symbol]; ok {
			return fault
		}
	}
	return nil
}

// moduleMembers is one module view's members of a symbol list, in the
// list's order — the one grouping the proof capture and the per-target
// narrowing share, so a sibling is always derived over exactly the
// subjects the caller named.
type moduleMembers struct {
	module   *moduleSubjectView
	views    []*subjectView
	subjects []gofresh.Subject
}

// groupByModule groups the named symbols by their module view, in
// first-seen order. Symbols the set lacks are skipped (a caller that
// must refuse them checks membership first); a repeated symbol repeats
// its subject, which the sibling derivation deduplicates by its own
// scope recipe (a target named in its own oracle is the common case).
func (s *subjectViewSet) groupByModule(symbols []string) []*moduleMembers {
	var order []*moduleMembers
	groups := map[*moduleSubjectView]*moduleMembers{}
	for _, symbol := range symbols {
		sv, ok := s.bySymbol[symbol]
		if !ok {
			continue
		}
		group, ok := groups[sv.module]
		if !ok {
			group = &moduleMembers{module: sv.module}
			groups[sv.module] = group
			order = append(order, group)
		}
		group.views = append(group.views, sv)
		group.subjects = append(group.subjects, sv.subject)
	}
	return order
}

// observedViewSet is a view set whose fingerprints carry the
// observation proof: the producer union. It is the only set a
// per-target narrowing derives from, so a narrowing over uncaptured
// proofs — whose evidence attachment would be refused after the
// measurement — is unrepresentable.
type observedViewSet struct {
	*subjectViewSet
}

// observed derives the producer union from the set's own views: per
// module view, one full-scope sibling (sharing the view's one
// observation — no second construction) carries the observation proof
// batch, captured once for the sibling's whole subject set. The
// decision view stays base-only, so the run-end and plan-end
// validations compare the base facts the decision read; the proof
// sibling validates through the observed arm — on the union path via
// each measured target's own narrowing derived from it, on the
// per-target rebuild path directly (REQ-exec-quiescence). A capture
// fault faults that module's symbols, never the union.
func (s *subjectViewSet) observed(ctx context.Context) (*observedViewSet, map[string]error, error) {
	faults := map[string]error{}
	union := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(s.bySymbol))}
	symbols := make([]string, 0, len(s.bySymbol))
	for symbol := range s.bySymbol {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	if observedUnionHook != nil {
		observedUnionHook(symbols)
	}
	for _, group := range s.groupByModule(symbols) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		views := group.views
		moduleFault := func(err error) {
			for _, sv := range views {
				faults[sv.symbol] = err
			}
		}
		proofView, err := group.module.view.Sibling(group.subjects)
		if err != nil {
			moduleFault(err)
			continue
		}
		observedFingerprints, err := proofView.CaptureObservedBatch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			moduleFault(err)
			continue
		}
		proofModule := &moduleSubjectView{view: proofView, validate: proofView.Validate}
		union.modules = append(union.modules, proofModule)
		for _, sv := range views {
			captured, ok := observedFingerprints[sv.subject]
			if !ok {
				faults[sv.symbol] = fmt.Errorf("gomutant: batched observation capture omitted subject %s.%s", sv.subject.Package, sv.subject.Symbol)
				continue
			}
			copied := *sv
			copied.fp, copied.view, copied.module = captured, proofView, proofModule
			union.bySymbol[sv.symbol] = &copied
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return &observedViewSet{union}, faults, nil
}

// forTarget narrows the union to one target's proof surface — the
// target and its oracle symbols, and exactly their modules — as
// sibling views derived from the union: each narrowing shares the
// union's one observation (identical fingerprints and facts) while
// owning its producer transaction, so one target's runtime-evidence
// attachment and validation seal never collide with a sibling
// target's (gofresh's per-view attach-once and seal-on-validate). A
// missing symbol returns its union fault (or a resolution miss),
// never a partial narrowing; a sibling derivation failure is
// target-local like any evidence-construction fault.
func (s *observedViewSet) forTarget(target string, oracle []string, faults map[string]error) (*subjectViewSet, error) {
	narrowed := &subjectViewSet{bySymbol: make(map[string]*subjectView, 1+len(oracle))}
	symbols := append([]string{target}, oracle...)
	for _, symbol := range symbols {
		if _, ok := s.bySymbol[symbol]; !ok {
			if err, faulted := faults[symbol]; faulted {
				return nil, err
			}
			return nil, fmt.Errorf("union view set carries no subject %s", symbol)
		}
	}
	// narrowed.bySymbol is populated only from sibling derivations below:
	// a raw union-backed entry would share the union's attach-once state
	// and seal — the exact collision this narrowing exists to prevent.
	for _, group := range s.groupByModule(symbols) {
		sibling, err := group.module.view.Sibling(group.subjects)
		if err != nil {
			return nil, err
		}
		siblingModule := &moduleSubjectView{view: sibling, validate: sibling.Validate}
		narrowed.modules = append(narrowed.modules, siblingModule)
		for _, sv := range group.views {
			narrowed.bySymbol[sv.symbol] = &subjectView{
				symbol: sv.symbol, subject: sv.subject, moduleDir: sv.moduleDir, evidenceDir: sv.evidenceDir,
				env: sv.env, view: sibling, fp: sv.fp, sourceFiles: sv.sourceFiles, module: siblingModule,
			}
		}
	}
	return narrowed, nil
}

func (t *Tree) newSubjectView(symbol string) (*subjectView, error) {
	views, err := t.newSubjectViews(context.Background(), []string{symbol}, false)
	if err != nil {
		return nil, err
	}
	return views.bySymbol[symbol], nil
}

func (s *subjectViewSet) validateProducers(ctx context.Context) error {
	for _, module := range s.modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if module.producer {
			if err := module.validate(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// acceptValidVerdict is the one matching predicate: only a plainly valid
// gofresh verdict lets a recorded subject stand. The killer-drift gate
// reaches the same bar by refreshing a target-package subject's compartment
// pin before checking — the refresh its attributable ledger diff licenses
// (REQ-result-stale's killer-drift carve-out) — never by tolerating a stale
// verdict, behind whose "test variants" reason a moved pin could hide.
func acceptValidVerdict(verdict gofresh.Verdict) bool {
	return verdict.Status == gofresh.Valid
}

// evidencePrecheck runs the pre-verdict evidence checks — identity, runtime
// state, purity — shared by the per-subject and batched walks. A record
// failing here never consults a gofresh verdict at all.
func (s *subjectView) evidencePrecheck(ctx context.Context, evidence SubjectEvidence, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	if evidence.Symbol != s.symbol || evidence.RuntimeInputs == "" || evidence.RuntimeDigest == "" {
		return false, nil
	}
	state, err := current(ctx, evidence.RuntimeInputs, evidenceBase(s.evidenceDir, evidence), s.env)
	if err != nil && ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil || !state.OK || state.Digest != evidence.RuntimeDigest ||
		state.Unverifiable != evidence.RuntimeUnverifiable || state.Reason != evidence.RuntimeReason {
		return false, nil
	}
	if evidence.RuntimeUnverifiable {
		return false, nil
	}
	if evidence.PurityAssertion != s.fp.PurityAssertion {
		return false, nil
	}
	return true, nil
}

// evidencePair binds one subject view to its recorded evidence and the
// verdict predicate its caller accepts for that subject.
type evidencePair struct {
	subject  *subjectView
	evidence SubjectEvidence
	accept   func(gofresh.Verdict) bool
}

// evidencePairsValid reports whether every pair's recorded evidence holds
// against the current tree. The pre-verdict checks run per subject; the
// gofresh verdicts then resolve through one CheckObservedBatch per view —
// the batch shares one runtime-input window across the view's subjects where
// a per-subject walk pays one window per check, and gofresh guarantees each
// subject's batched verdict equals its single CheckObserved. Evidence
// without observation facts keeps the per-subject check, as does a subject
// appearing twice in one view — the batch map cannot carry two recordings
// for one subject.
func evidencePairsValid(ctx context.Context, pairs []evidencePair, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	for _, pair := range pairs {
		ok, err := pair.subject.evidencePrecheck(ctx, pair.evidence, current)
		if err != nil || !ok {
			return false, err
		}
	}
	type viewBatch struct {
		recorded map[gofresh.Subject]gofresh.Fingerprint
		members  []int
	}
	batches := map[*gofresh.View]*viewBatch{}
	order := make([]*gofresh.View, 0, len(pairs))
	for i, pair := range pairs {
		fingerprint := pair.evidence.fingerprint()
		single := !observedFingerprint(fingerprint)
		batch := batches[pair.subject.view]
		if !single && batch != nil {
			_, single = batch.recorded[pair.subject.subject]
		}
		if single {
			verdict, err := pair.subject.checkContext(ctx, fingerprint)
			if err != nil {
				return false, err
			}
			if !pair.accept(verdict) {
				return false, nil
			}
			continue
		}
		if batch == nil {
			batch = &viewBatch{recorded: map[gofresh.Subject]gofresh.Fingerprint{}}
			batches[pair.subject.view] = batch
			order = append(order, pair.subject.view)
		}
		batch.recorded[pair.subject.subject] = fingerprint
		batch.members = append(batch.members, i)
	}
	for _, view := range order {
		batch := batches[view]
		verdicts, err := view.CheckObservedBatch(ctx, batch.recorded)
		if err != nil {
			return false, err
		}
		for _, i := range batch.members {
			if !pairs[i].accept(verdicts[pairs[i].subject.subject]) {
				return false, nil
			}
		}
	}
	return true, nil
}

func (s *subjectView) inspect(evidence SubjectEvidence) (FindingInspection, error) {
	return s.inspectContext(context.Background(), evidence)
}

func (s *subjectView) inspectContext(ctx context.Context, evidence SubjectEvidence) (FindingInspection, error) {
	if evidence.Symbol != s.symbol {
		return FindingInspection{State: FindingStale, Reason: "subject identity changed"}, nil
	}
	if evidence.RuntimeUnverifiable {
		return FindingInspection{State: FindingUnverifiable, Reason: evidence.RuntimeReason}, nil
	}
	state, err := runtimeinput.CurrentEnvContext(ctx, evidence.RuntimeInputs, evidenceBase(s.evidenceDir, evidence), s.env)
	if err != nil || !state.OK {
		if ctx.Err() != nil {
			return FindingInspection{}, ctx.Err()
		}
		if err != nil {
			return FindingInspection{State: FindingUnverifiable, Reason: err.Error()}, nil
		}
		return FindingInspection{State: FindingUnverifiable, Reason: "runtime inputs cannot be evaluated"}, nil
	}
	if state.Unverifiable {
		return FindingInspection{State: FindingUnverifiable, Reason: state.Reason}, nil
	}
	if state.Digest != evidence.RuntimeDigest {
		return FindingInspection{State: FindingStale, Reason: "runtime inputs changed" + movedInputSuffix(ctx, evidence.RuntimeInputs, evidenceBase(s.evidenceDir, evidence), s.env)}, nil
	}
	if evidence.PurityAssertion != s.fp.PurityAssertion {
		return FindingInspection{State: FindingStale, Reason: "purity assertion changed"}, nil
	}
	verdict, err := s.checkContext(ctx, evidence.fingerprint())
	if err != nil {
		return FindingInspection{}, err
	}
	switch verdict.Status {
	case gofresh.Valid:
		return FindingInspection{State: FindingCurrent}, nil
	case gofresh.Unverifiable:
		return FindingInspection{State: FindingUnverifiable, Reason: verdict.Reason}, nil
	default:
		return FindingInspection{State: FindingStale, Reason: verdict.Reason}, nil
	}
}

// movedInputSuffix names the moved runtime-input identities behind a
// digest drift, so the developer sees WHICH observed input moved, not
// just that one did (REQ-result-inspection). Attribution is
// best-effort: an unwalkable manifest keeps the generic reason.
func movedInputSuffix(ctx context.Context, encoded, moduleDir string, env []string) string {
	moved, err := runtimeinput.MovedInputsContext(ctx, encoded, moduleDir, env)
	if err != nil || len(moved) == 0 {
		return ""
	}
	const show = 3
	if len(moved) > show {
		return ": " + strings.Join(moved[:show], ", ") + fmt.Sprintf(" and %d more", len(moved)-show)
	}
	return ": " + strings.Join(moved, ", ")
}

// observedFingerprint reports whether a recorded fingerprint carries
// observation facts and so must be checked under the explicit observed
// policy — the single routing predicate for the per-subject check and the
// batched walk.
func observedFingerprint(fingerprint gofresh.Fingerprint) bool {
	return fingerprint.ObservationAssertion != "" || fingerprint.ObservationProof != (gofresh.ObservationProof{})
}

func (s *subjectView) checkContext(ctx context.Context, fingerprint gofresh.Fingerprint) (gofresh.Verdict, error) {
	if observedFingerprint(fingerprint) {
		return s.view.CheckObserved(ctx, fingerprint, s.subject)
	}
	return s.view.Check(ctx, fingerprint, s.subject)
}

// InspectFinding is InspectFindingContext without caller-owned
// cancellation.
func (t *Tree) InspectFinding(f Finding) (FindingInspection, error) {
	return t.InspectFindingContext(context.Background(), f)
}

// InspectFindingContext is InspectFindingsContext over one record.
func (t *Tree) InspectFindingContext(ctx context.Context, f Finding) (FindingInspection, error) {
	inspections, err := t.InspectFindingsContext(ctx, []Finding{f}, nil)
	if err != nil {
		return FindingInspection{}, err
	}
	return inspections[0], nil
}

// InspectFindingsContext judges every record of a document in one pass
// over the records' shared subject views: each record's view-free
// pre-checks run first, over one declared-symbol walk and one oracle
// validation per distinct oracle; the subjects the undecided records'
// judgments read are built once per package-process posture; and every
// record is judged against that set — so the cost scales with the
// distinct subjects the records name, never with the record count
// (REQ-result-inspection). A record whose judgment fails fails the
// pass, as a single-record inspection would. progress, when given,
// names each stage as it begins (the admission, the view build, each
// record's judgment).
func (t *Tree) InspectFindingsContext(ctx context.Context, findings []Finding, progress func(stage string)) ([]FindingInspection, error) {
	inspections, errs, err := t.inspectFindings(ctx, findings, progress, false)
	if err != nil {
		return nil, err
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return inspections, nil
}

// inspectFindings is the batched judgment with a per-record error
// boundary: errs[i] is record i's own failure (its inspection then
// zero), err a failure of the pass itself (cancellation, the tree's
// declared symbols). A best-effort reader (the closure signpost) takes
// every record's outcome; a judged view stops at the first record
// error, as its per-record loop did.
func (t *Tree) inspectFindings(ctx context.Context, findings []Finding, progress func(stage string), bestEffort bool) ([]FindingInspection, []error, error) {
	report := func(stage string) {
		if progress != nil {
			progress(stage)
		}
	}
	report(fmt.Sprintf("admitting %d record(s)", len(findings)))
	shared := t.newAdmissionShared()
	admissions := make([]judgmentAdmission, len(findings))
	errs := make([]error, len(findings))
	subjects := map[bool]map[string]bool{false: {}, true: {}}
	for i, f := range findings {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		admissions[i], errs[i] = t.admitFindingContext(ctx, f, shared)
		if errs[i] != nil {
			if !bestEffort {
				return nil, errs, nil
			}
			continue
		}
		for _, symbol := range admissions[i].symbols {
			subjects[findingPackageProcessAttestable(f)][symbol] = true
		}
	}
	prebuilt := map[bool]*subjectViewSet{}
	for _, packageProcess := range []bool{false, true} {
		set := subjects[packageProcess]
		if len(set) == 0 {
			continue
		}
		symbols := make([]string, 0, len(set))
		for symbol := range set {
			symbols = append(symbols, symbol)
		}
		sort.Strings(symbols)
		report(fmt.Sprintf("building views for %d subject(s)", len(symbols)))
		// Fault-tolerant: a subject the build cannot serve stays out of
		// the set, and the record reading it builds its own view — the
		// per-record judgment's own path, failing that record alone.
		views, _, err := t.buildSubjectViews(ctx, symbols, t.eng.PackageContextContext, t.newSubjectEngines(nil, packageProcess))
		if err != nil {
			return nil, nil, err
		}
		prebuilt[packageProcess] = views
	}
	out := make([]FindingInspection, len(findings))
	for i, f := range findings {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if errs[i] != nil {
			continue
		}
		report("judging " + f.Symbol)
		inspection, err := t.judgeAdmittedContext(ctx, f, admissions[i], prebuilt[findingPackageProcessAttestable(f)])
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			errs[i] = err
			if !bestEffort {
				return nil, errs, nil
			}
			continue
		}
		out[i] = withCandidateEvidence(inspection, f)
	}
	return out, errs, ctx.Err()
}

// withCandidateEvidence carries the record's candidate evidence on the
// inspection: the state answers "can this record be reused as it
// stands"; flagged candidates mean it cannot — they re-execute before
// any serve — so a record otherwise current classifies unverifiable
// with the candidate evidence carrying the specifics
// (REQ-result-inspection).
func withCandidateEvidence(inspection FindingInspection, f Finding) FindingInspection {
	inspection.CandidateEvidence = canonicalCandidateEvidence(f.CandidateEvidence)
	if inspection.State == FindingCurrent && len(inspection.CandidateEvidence) != 0 {
		inspection.State = FindingUnverifiable
		inspection.Reason = fmt.Sprintf("%d candidate(s) carry unverifiable runtime evidence and re-execute before reuse", len(inspection.CandidateEvidence))
	}
	return inspection
}

// admissionShared is what a pass's pre-checks share: the tree's
// declared symbols, walked once, and each oracle symbol's validity,
// checked once — the two whole-tree walks a per-record admission would
// otherwise repeat per record.
type admissionShared struct {
	t           *Tree
	declared    []string
	walked      bool
	oracleValid map[string]bool
}

func (t *Tree) newAdmissionShared() *admissionShared {
	return &admissionShared{t: t, oracleValid: map[string]bool{}}
}

// declares walks the declared symbols on first use — a pass of shaped
// records alone never walks them.
func (s *admissionShared) declares(ctx context.Context, symbol string) (bool, error) {
	if !s.walked {
		declared, err := s.t.eng.DeclaredSymbolsContext(ctx)
		if err != nil {
			return false, err
		}
		s.declared, s.walked = declared, true
	}
	i := sort.SearchStrings(s.declared, symbol)
	return i < len(s.declared) && s.declared[i] == symbol, nil
}

func (s *admissionShared) validOracle(ctx context.Context, symbol string) bool {
	if valid, ok := s.oracleValid[symbol]; ok {
		return valid
	}
	valid := s.t.eng.ValidateOracleContext(ctx, []string{symbol}) == nil
	s.oracleValid[symbol] = valid
	return valid
}

// judgmentAdmission is what a record's view-free pre-checks decide: a
// final inspection when no view is needed (with the derived-oracle
// delta's enrichment still owed from the target's ledger), or the
// subjects whose views decide the record — the mutated symbol (symbol
// records) and the recorded oracle tests that still validate.
type judgmentAdmission struct {
	decided     *FindingInspection
	enrichDelta []string // the derived oracle's current tests, when the delta enrichment reads the target's ledger
	target      string
	oracle      []SubjectEvidence
	validOracle map[string]bool
	symbols     []string
}

// admitFindingContext runs a record's pre-checks over the shared walk —
// the one admission both the batched pass and the per-record judgment
// consult, so the views a pass builds are exactly the views its records
// then read.
func (t *Tree) admitFindingContext(ctx context.Context, f Finding, shared *admissionShared) (judgmentAdmission, error) {
	var adm judgmentAdmission
	decided := func(inspection FindingInspection) (judgmentAdmission, error) {
		adm.decided = &inspection
		return adm, nil
	}
	if f.Shape != nil {
		if f.OperatorSet != shapedOperatorSet {
			return decided(FindingInspection{State: FindingStale, Reason: "shaped operator set changed"})
		}
		if _, err := time.ParseDuration(f.OracleTimeout); err != nil {
			return adm, fmt.Errorf("finding %s has invalid oracle timeout: %w", f.Symbol, err)
		}
		_, digest, _, err := t.shapedCandidates(ctx, Target{Symbol: f.Symbol, Structural: f.Shape.Structural, Manual: f.Shape.Manual, Oracle: nil, OracleExplicit: true})
		if err != nil {
			if ctx.Err() != nil {
				return adm, ctx.Err()
			}
			return decided(FindingInspection{State: FindingStale, Reason: "shape no longer derives: " + err.Error()})
		}
		if digest != f.BodyHash {
			return decided(FindingInspection{State: FindingStale, Reason: "the declared shape or a probed file moved"})
		}
	} else {
		declared, err := shared.declares(ctx, f.Symbol)
		if err != nil {
			return adm, err
		}
		if !declared {
			return decided(FindingInspection{State: FindingDetached, Reason: "mutated symbol no longer resolves - terminal: no re-measure can revive this record; prune removes it, retarget follows a rename"})
		}
		if f.OperatorSet != engine.OperatorSet {
			return decided(FindingInspection{State: FindingStale, Reason: "operator set changed"})
		}
		if _, err := time.ParseDuration(f.OracleTimeout); err != nil {
			return adm, fmt.Errorf("finding %s has invalid oracle timeout: %w", f.Symbol, err)
		}
		adm.target = f.Symbol
		if !f.OracleExplicit {
			currentOracle, err := t.resolveOracleContext(ctx, Target{Symbol: f.Symbol})
			if err != nil {
				return adm, err
			}
			recordedOracle := make([]string, len(f.OracleEvidence))
			for i, evidence := range f.OracleEvidence {
				recordedOracle[i] = evidence.Symbol
			}
			sort.Strings(recordedOracle)
			if reason := derivedOracleDelta(recordedOracle, currentOracle); reason != "" {
				// The enrichment reads the target's ledger only when the
				// record carries one and some recorded oracle test is still
				// in the derived set — otherwise it names nothing, and the
				// view is not admitted.
				if f.CompartmentLedger != nil && len(retainedOracleNames(f, currentOracle)) > 0 {
					adm.enrichDelta, adm.symbols = currentOracle, []string{f.Symbol}
				}
				return decided(FindingInspection{State: FindingStale, Reason: reason})
			}
		}
		adm.symbols = append(adm.symbols, f.Symbol)
	}
	adm.oracle = sortedSubjectEvidence(f.OracleEvidence)
	adm.validOracle = make(map[string]bool, len(adm.oracle))
	for _, evidence := range adm.oracle {
		if shared.validOracle(ctx, evidence.Symbol) {
			adm.validOracle[evidence.Symbol] = true
			adm.symbols = append(adm.symbols, evidence.Symbol)
		}
	}
	return adm, nil
}

// judgeAdmittedContext finishes an admitted record's judgment over the
// views it reads — the prebuilt set where it serves the subject, a
// supplementary view otherwise (the per-record judgment's own path,
// reported through inspectionSupplementaryViewHook).
func (t *Tree) judgeAdmittedContext(ctx context.Context, f Finding, adm judgmentAdmission, prebuilt *subjectViewSet) (FindingInspection, error) {
	if adm.decided != nil {
		inspection := *adm.decided
		if adm.enrichDelta != nil {
			if modified := t.modifiedOracleNames(ctx, f, adm.enrichDelta, prebuilt); len(modified) > 0 {
				inspection.Reason = strings.TrimSuffix(inspection.Reason, ")") + "; modified: " + cappedNameList(modified, "tests") + ")"
			}
		}
		return inspection, nil
	}
	viewFor, err := t.viewsFor(ctx, adm.symbols, prebuilt, findingPackageProcessAttestable(f))
	if err != nil {
		return FindingInspection{}, err
	}
	if adm.target != "" {
		inspection, err := viewFor[adm.target].inspectContext(ctx, f.TargetEvidence)
		if err != nil || inspection.State != FindingCurrent {
			if err == nil && inspection.Reason != "" {
				inspection.Reason = "target: " + inspection.Reason
			}
			return inspection, err
		}
	}
	for _, evidence := range adm.oracle {
		if err := ctx.Err(); err != nil {
			return FindingInspection{}, err
		}
		if !adm.validOracle[evidence.Symbol] {
			return FindingInspection{State: FindingStale, Reason: "oracle " + evidence.Symbol + " no longer resolves"}, nil
		}
		inspection, err := viewFor[evidence.Symbol].inspectContext(ctx, evidence)
		if err != nil {
			return FindingInspection{}, err
		}
		if inspection.State != FindingCurrent {
			inspection.Reason = "oracle " + evidence.Symbol + ": " + inspection.Reason
			return inspection, nil
		}
	}
	return FindingInspection{State: FindingCurrent}, nil
}

// viewsFor serves each symbol's view from the prebuilt set, building
// one supplementary set for the rest.
func (t *Tree) viewsFor(ctx context.Context, symbols []string, prebuilt *subjectViewSet, packageProcess bool) (map[string]*subjectView, error) {
	viewFor := make(map[string]*subjectView, len(symbols))
	var missing []string
	for _, symbol := range symbols {
		if prebuilt != nil {
			if view, ok := prebuilt.bySymbol[symbol]; ok {
				viewFor[symbol] = view
				continue
			}
		}
		missing = append(missing, symbol)
	}
	if len(missing) > 0 {
		if inspectionSupplementaryViewHook != nil {
			inspectionSupplementaryViewHook(missing)
		}
		supplementary, err := t.newSubjectViews(ctx, missing, packageProcess)
		if err != nil {
			return nil, err
		}
		for symbol, view := range supplementary.bySymbol {
			viewFor[symbol] = view
		}
	}
	return viewFor, nil
}

func canonicalCandidateEvidence(evidence []CandidateEvidence) []CandidateEvidence {
	sorted := append([]CandidateEvidence(nil), evidence...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Position != sorted[j].Position {
			return sorted[i].Position < sorted[j].Position
		}
		return sorted[i].Operator < sorted[j].Operator
	})
	return sorted
}

// inspectionSupplementaryViewHook observes the supplementary view build for
// symbols a caller-supplied prebuilt set does not cover — the event tests pin
// to prove the run's stale-reason enrichment reuses the run's own views.
var inspectionSupplementaryViewHook func(symbols []string)

// inspectFindingStateContext classifies a record against the current tree.
// A non-nil prebuilt view set serves the symbols it covers — the run's
// stale-reason enrichment passes the views the serve decision itself just
// used, so attribution reads the same observation instead of paying a second
// package-load-scale construction per stale target (REQ-result-stale's
// naming arm); symbols the prebuilt set lacks (a recorded oracle the current
// target no longer names) build one supplementary set.
func (t *Tree) inspectFindingStateContext(ctx context.Context, f Finding, prebuilt *subjectViewSet) (FindingInspection, error) {
	if err := ctx.Err(); err != nil {
		return FindingInspection{}, err
	}
	adm, err := t.admitFindingContext(ctx, f, t.newAdmissionShared())
	if err != nil {
		return FindingInspection{}, err
	}
	return t.judgeAdmittedContext(ctx, f, adm, prebuilt)
}

func sortedSubjectEvidence(evidence []SubjectEvidence) []SubjectEvidence {
	sorted := append([]SubjectEvidence(nil), evidence...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Symbol < sorted[j].Symbol })
	return sorted
}

// attestationPinView strips audit-only metadata from subject evidence
// before the attestation-pin comparison: the recorded dynamic-state
// vouches are the labels precedent - correlation and audit data, never
// a measurement pin - so a vouch-set change alone never sheds a
// disposition whose every measured pin still holds.
func attestationPinView(evidence SubjectEvidence) SubjectEvidence {
	evidence.DynamicStateVouches = ""
	evidence.PackageProcessDischarges = ""
	// ModuleBase is resolution metadata for the store's portable-line
	// walk, never a measured pin: a record grown the field on its first
	// post-upgrade measure must not shed its dispositions over it.
	evidence.ModuleBase = ""
	return evidence
}

// mutationDomainHeld reports whether two findings describe the same
// mutation domain: the mutated body and the operator grammar that
// generates candidates from it. Candidate identity is budget-independent
// (INV-RESULT-CANDIDATE-CONSERVATION assigns occurrence suffixes over the
// complete ordered set before budget selection), so body hash and
// operator set together decide whether a recorded position+operator
// names the same mutant today - the identity an equivalence disposition
// is a judgment about (REQ-attest-survivor).
func mutationDomainHeld(prior, current Finding) bool {
	return prior.BodyHash == current.BodyHash && prior.OperatorSet == current.OperatorSet
}

// effectiveCeiling maps a recorded or current oracle-memory pin to
// its comparable bound: a non-positive pin means no ceiling.
func effectiveCeiling(bytes int64) int64 {
	if bytes <= 0 {
		return math.MaxInt64
	}
	return bytes
}

// memoryPinStale reports whether the record's oracle-memory pin
// refuses reuse under the current effective ceiling. A ceiling-decided
// record pins its exact bytes — the ceiling authored at least one of
// its verdicts, so any different ceiling could flip one. Every other
// record serves directionally: a current ceiling at least as large as
// the recorded one preserves each verdict (a pass at the recorded
// ceiling still fits, a non-memory kill still fires), while a smaller
// one could newly exhaust an execution and must re-measure
// (REQ-result-stale, REQ-exec-oracle-memory).
func memoryPinStale(prior Finding, currentPin int64) bool {
	if prior.OracleCeilingDecided {
		return prior.OracleMemoryBytes != currentPin
	}
	return effectiveCeiling(currentPin) < effectiveCeiling(prior.OracleMemoryBytes)
}

// timeoutPinMatches compares a record's oracle-bound pin against the
// current run's posture: exact agreement, or derived on both sides
// with every timeout kill re-executable — derived budgets vary with
// the measured tree, and re-pinning to them would churn every record
// every run for bounds that cannot change any verdict. The relaxation
// covers every verdict class it admits: a completed verdict is an
// answer about the suite (tests pass or fail on the mutant), and a
// budget change can only turn answers into refusals to answer, never
// into different answers; a "(timeout)"-attributed kill is a claim
// about the bound itself, admitted only where its candidate-local
// incomplete-observation evidence rides the record — the flagged serve
// discipline then re-executes exactly that candidate under the current
// derived budget, re-vouching the bound claim by measurement. A
// timeout kill without that evidence (a structural-shaped record's,
// whose serve is wholesale and carries no candidate evidence) has no
// re-execution route, so the pin refuses and the record re-measures
// (REQ-result-stale's timeout-kill rule).
func timeoutPinMatches(prior Finding, timeout string, derived bool) bool {
	if prior.OracleTimeout == timeout && prior.OracleTimeoutDerived == derived {
		return true
	}
	return prior.OracleTimeoutDerived && derived && timeoutKillsReexecutable(prior)
}

// timeoutKillsReexecutable reports every "(timeout)"-attributed kill
// carrying its candidate-local evidence row — the re-vouching route the
// relaxed derived pin rides.
func timeoutKillsReexecutable(f Finding) bool {
	for _, k := range f.Kills {
		if k.Killer != TimeoutKiller {
			continue
		}
		covered := false
		for _, ev := range f.CandidateEvidence {
			if ev.Position == k.Position && ev.Operator == k.Operator {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// timeoutPinMatchesRecord is timeoutPinMatches for record-to-record
// comparisons (the attestation-carry pins).
func timeoutPinMatchesRecord(prior, current Finding) bool {
	return timeoutPinMatches(prior, current.OracleTimeout, current.OracleTimeoutDerived)
}

func sameAttestationPins(prior, current Finding) bool {
	if prior.PropertyRegime != current.PropertyRegime {
		return false
	}
	if prior.OperatorSet != current.OperatorSet || prior.OracleExplicit != current.OracleExplicit || prior.Budget != current.Budget ||
		prior.CandidateCount != current.CandidateCount || prior.Generated != current.Generated ||
		!timeoutPinMatchesRecord(prior, current) || memoryPinStale(prior, current.OracleMemoryBytes) || attestationPinView(prior.TargetEvidence) != attestationPinView(current.TargetEvidence) ||
		len(prior.OracleEvidence) != len(current.OracleEvidence) {
		return false
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return false
		}
		bySymbol[evidence.Symbol] = evidence
	}
	seen := make(map[string]bool, len(current.OracleEvidence))
	for _, evidence := range current.OracleEvidence {
		if seen[evidence.Symbol] {
			return false
		}
		seen[evidence.Symbol] = true
		if priorEvidence, ok := bySymbol[evidence.Symbol]; !ok || attestationPinView(priorEvidence) != attestationPinView(evidence) {
			return false
		}
	}
	return true
}

// cappedNameList renders an identity list for a reason string: the
// count and the first exemplars carry the signal, the full list rides
// the detail surfaces - best-effort naming per REQ-result-inspection.
func cappedNameList(names []string, noun string) string {
	const exemplars = 3
	if len(names) <= exemplars {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%d %s: %s, ... (+%d more)", len(names), noun, strings.Join(names[:exemplars], ", "), len(names)-exemplars)
}

// derivedOracleDelta names how the current derived oracle set departs from
// the recorded one — the added and removed test identities — or returns ""
// when the sets are equal. Naming the delta keeps the re-measure decision
// legible: a caller who just wrote kill-tests sees the tool noticing them,
// and a shrink is loud enough to question (a test the record was measured
// against no longer exists). Both slices must be sorted.
func derivedOracleDelta(recorded, current []string) string {
	recordedSet := make(map[string]bool, len(recorded))
	for _, symbol := range recorded {
		recordedSet[symbol] = true
	}
	currentSet := make(map[string]bool, len(current))
	for _, symbol := range current {
		currentSet[symbol] = true
	}
	var added, removed []string
	for _, symbol := range current {
		if !recordedSet[symbol] {
			added = append(added, symbol)
		}
	}
	for _, symbol := range recorded {
		if !currentSet[symbol] {
			removed = append(removed, symbol)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		if len(recorded) != len(current) {
			// Same identities, different multiplicity: a recorded oracle
			// repeating an identity is malformed evidence, never equality.
			return "derived oracle changed (recorded oracle repeats an identity)"
		}
		return ""
	}
	var parts []string
	if len(added) != 0 {
		parts = append(parts, "added: "+cappedNameList(added, "tests"))
	}
	if len(removed) != 0 {
		parts = append(parts, "removed: "+cappedNameList(removed, "tests"))
	}
	return "derived oracle changed (" + strings.Join(parts, "; ") + ")"
}

// retainedOracleNames maps the test names of the record's oracle tests
// that are still in the current derived oracle to their symbols — the
// tests a ledger diff can name as modified.
func retainedOracleNames(f Finding, currentOracle []string) map[string]string {
	currentSet := make(map[string]bool, len(currentOracle))
	for _, symbol := range currentOracle {
		currentSet[symbol] = true
	}
	byName := map[string]string{}
	for _, evidence := range f.OracleEvidence {
		if !currentSet[evidence.Symbol] {
			continue
		}
		if _, fn := splitTestSymbol(evidence.Symbol); fn != "" {
			byName[fn] = evidence.Symbol
		}
	}
	return byName
}

// modifiedOracleNames names the surviving oracle tests whose BODIES
// changed - the "modified:" arm of the derived-oracle-delta reason
// (REQ-result-inspection's naming arm). The instrument is the recorded
// compartment ledger diffed against the current one: per-declaration
// hashes attribute the edit to the exact test, where any view-level
// verdict cannot (the test-variant compartment is package-shared, so
// every sibling test reads compartment-stale on any test edit, and a
// test's own body lives in that same compartment). Best-effort: a
// record predating the ledger, or any derivation error, names nothing
// rather than failing a decision that is already stale. The target
// view comes from the caller's prebuilt set where present - the same
// second-construction avoidance the enclosing inspection documents.
func (t *Tree) modifiedOracleNames(ctx context.Context, f Finding, currentOracle []string, prebuilt *subjectViewSet) []string {
	if f.CompartmentLedger == nil {
		return nil
	}
	byName := retainedOracleNames(f, currentOracle)
	if len(byName) == 0 {
		return nil
	}
	var target *subjectView
	if prebuilt != nil {
		target = prebuilt.bySymbol[f.Symbol]
	}
	if target == nil {
		views, err := t.newSubjectViews(ctx, []string{f.Symbol}, findingPackageProcessAttestable(f))
		if err != nil {
			return nil
		}
		target = views.bySymbol[f.Symbol]
		if target == nil {
			return nil
		}
	}
	currentLedger, err := target.view.TestVariantLedger(target.subject)
	if err != nil {
		return nil
	}
	delta := gofresh.DiffTestVariantLedgers(f.CompartmentLedger.ledger(), currentLedger)
	var modified []string
	seen := map[string]bool{}
	for _, change := range delta.Changed {
		symbol, ok := byName[change.After.Name]
		if !ok || seen[symbol] {
			continue
		}
		seen[symbol] = true
		modified = append(modified, symbol)
	}
	sort.Strings(modified)
	return modified
}

// runtimeMemo memoizes current-runtime-state evaluations per (manifest,
// module, environment) and re-verifies any state used more than once, so one
// matching pass reads each manifest exactly once and a manifest that moved
// mid-evaluation refuses.
type runtimeMemo struct {
	current func(context.Context, string, string, []string) (runtimeinput.State, error)
	results map[runtimeMemoKey]*runtimeMemoResult
	order   []runtimeMemoKey
}

type runtimeMemoKey struct {
	manifest, moduleDir, environment string
}

type runtimeMemoResult struct {
	state runtimeinput.State
	err   error
	env   []string
	uses  int
}

func newRuntimeMemo(current func(context.Context, string, string, []string) (runtimeinput.State, error)) *runtimeMemo {
	return &runtimeMemo{current: current, results: map[runtimeMemoKey]*runtimeMemoResult{}}
}

func (m *runtimeMemo) once(ctx context.Context, manifest, moduleDir string, env []string) (runtimeinput.State, error) {
	key := runtimeMemoKey{manifest: manifest, moduleDir: moduleDir, environment: sequenceKey(env)}
	if result, ok := m.results[key]; ok {
		result.uses++
		return result.state, result.err
	}
	state, err := m.current(ctx, manifest, moduleDir, env)
	m.results[key] = &runtimeMemoResult{state: state, err: err, env: append([]string(nil), env...), uses: 1}
	m.order = append(m.order, key)
	return state, err
}

// verify re-reads every manifest that was consulted more than once and
// refuses when any moved during the evaluation.
func (m *runtimeMemo) verify(ctx context.Context) (bool, error) {
	for _, key := range m.order {
		result := m.results[key]
		if result.uses < 2 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		state, err := m.current(ctx, key.manifest, key.moduleDir, result.env)
		if err != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil || state != result.state {
			return false, nil
		}
	}
	return true, nil
}

// killerDriftAttributable reports whether a compartment delta is one the
// referenced-name walk can fully attribute (REQ-result-stale's killer-drift
// carve-out): every added, changed, or removed declaration is a plain
// function (never TestMain), a method of a compartment-declared receiver
// type, a const, or a type — kinds whose only route to an unchanged test is
// a reference chain the walk follows. The rejected kinds each reach
// unchanged code without any reference: a package var's initializer and an
// init function run during test-binary initialization, TestMain wraps every
// test, a directive is behavior-bearing from any position, a method of a
// receiver type declared outside the compartment can flip interface
// satisfaction observed by production code the ledger cannot see, and an
// embedded member's bytes feed unchanged code as data.
func killerDriftAttributable(delta gofresh.TestVariantDelta, recorded, current gofresh.TestVariantLedger) bool {
	// Types are keyed by declaring package: a method's receiver resolves
	// within its own package only, and the two compartment packages (the
	// in-package and external variants) may declare same-named types, so a
	// name-only match would let a method on a production type ride a
	// collision with the other variant's type. An entry without a package
	// (a recorded ledger persisted before the field) certifies nothing.
	compartmentTypes := map[string]bool{}
	noteTypes := func(declarations []gofresh.TestVariantDeclaration) {
		for _, declaration := range declarations {
			if declaration.Kind == "type" && declaration.Package != "" {
				compartmentTypes[declaration.Package+"\x00"+declaration.Name] = true
			}
		}
	}
	noteTypes(recorded.Declarations)
	noteTypes(current.Declarations)
	attributable := func(declaration gofresh.TestVariantDeclaration) bool {
		switch declaration.Kind {
		case "func":
			return declaration.Name != "TestMain"
		case "method":
			return declaration.Package != "" && compartmentTypes[declaration.Package+"\x00"+receiverBaseName(declaration.Receiver)]
		case "const", "type":
			return true
		default:
			return false
		}
	}
	for _, declaration := range delta.Added {
		if !attributable(declaration) {
			return false
		}
	}
	for _, declaration := range delta.Removed {
		if !attributable(declaration) {
			return false
		}
	}
	for _, change := range delta.Changed {
		if !attributable(change.Before) || !attributable(change.After) {
			return false
		}
	}
	for _, header := range delta.HeaderChanges {
		if header.Embedded {
			return false
		}
	}
	return true
}

// receiverBaseName reduces a receiver type's source text to its base type
// name: pointer markers, generic parameter lists, and surrounding space
// stripped ("*suite", "suite[T]", "*suite[K, V]" all reduce to "suite").
func receiverBaseName(receiver string) string {
	base := strings.TrimSpace(receiver)
	base = strings.TrimPrefix(base, "(")
	base = strings.TrimSuffix(base, ")")
	base = strings.TrimSpace(strings.TrimPrefix(base, "*"))
	if i := strings.IndexByte(base, '['); i >= 0 {
		base = base[:i]
	}
	return strings.TrimSpace(base)
}

// compartmentReach is the reference graph the killer-drift walk traverses:
// the current ledger's declarations (whose referenced-name lists speak for
// every unchanged declaration — equal hashes pin equal bytes) plus the
// delta's removed declarations as terminal nodes, so a walk reaching a
// removed method through its receiver type still observes the removal.
type compartmentReach struct {
	entries           []gofresh.TestVariantDeclaration
	byName            map[string][]int
	methodsByReceiver map[string][]int
	touchedEntries    map[int]bool
}

func newCompartmentReach(current gofresh.TestVariantLedger, delta gofresh.TestVariantDelta) *compartmentReach {
	reach := &compartmentReach{
		byName:            map[string][]int{},
		methodsByReceiver: map[string][]int{},
		touchedEntries:    map[int]bool{},
	}
	type identity struct{ file, kind, receiver, name, hash string }
	touched := map[identity]bool{}
	note := func(declaration gofresh.TestVariantDeclaration) {
		touched[identity{declaration.File, declaration.Kind, declaration.Receiver, declaration.Name, declaration.Hash}] = true
	}
	for _, declaration := range delta.Added {
		note(declaration)
	}
	for _, declaration := range delta.Removed {
		note(declaration)
	}
	for _, change := range delta.Changed {
		note(change.Before)
		note(change.After)
	}
	add := func(declaration gofresh.TestVariantDeclaration) {
		i := len(reach.entries)
		reach.entries = append(reach.entries, declaration)
		reach.byName[declaration.Name] = append(reach.byName[declaration.Name], i)
		if declaration.Kind == "method" {
			base := receiverBaseName(declaration.Receiver)
			reach.methodsByReceiver[base] = append(reach.methodsByReceiver[base], i)
		}
		if touched[identity{declaration.File, declaration.Kind, declaration.Receiver, declaration.Name, declaration.Hash}] {
			reach.touchedEntries[i] = true
		}
	}
	for _, declaration := range current.Declarations {
		add(declaration)
	}
	for _, declaration := range delta.Removed {
		add(declaration)
	}
	return reach
}

// reaches reports whether the test function fn can observe any delta
// declaration. One detection mechanism: the walk visits graph nodes — by
// referenced name, and through every method of a receiver type it reaches,
// reflection's only route to a compartment function — and flags on visiting
// a touched entry. Every delta declaration is a visitable node (removed
// ones ride in as terminal nodes), so name-level matching would be a
// redundant second mechanism, not extra coverage. known is false when fn
// has no compartment "func" declaration to start from — the walk cannot
// attribute and the caller must refuse. A visited function or method entry
// with no recorded references is treated as reaching (fail closed): every
// compiled declaration references at least its own name, so an empty list
// is a reference surface the current view did not serve — including every
// removed function or method, whose recorded ledger carries no references.
func (r *compartmentReach) reaches(fn string) (reached, known bool) {
	var seeds []int
	for _, i := range r.byName[fn] {
		if r.entries[i].Kind == "func" {
			seeds = append(seeds, i)
		}
	}
	if len(seeds) == 0 {
		return false, false
	}
	return r.walk(seeds), true
}

// unconditionalRootReaches reports whether any declaration that runs or
// wraps every test regardless of references — a package var's initializer,
// an init function, or TestMain — can reach a delta declaration. The
// license bars those kinds from the delta itself, but an unchanged
// initializer calling a changed plain function mutates state every test
// observes without any oracle's walk naming the change, so a reaching root
// refuses the carve-out outright.
func (r *compartmentReach) unconditionalRootReaches() bool {
	var seeds []int
	for i, entry := range r.entries {
		if entry.Kind == "var" || entry.Kind == "init" || (entry.Kind == "func" && entry.Name == "TestMain") {
			seeds = append(seeds, i)
		}
	}
	return r.walk(seeds)
}

func (r *compartmentReach) walk(seeds []int) bool {
	var queue []int
	visited := map[int]bool{}
	push := func(i int) {
		if !visited[i] {
			visited[i] = true
			queue = append(queue, i)
		}
	}
	for _, i := range seeds {
		push(i)
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		entry := r.entries[i]
		if r.touchedEntries[i] {
			return true
		}
		if (entry.Kind == "func" || entry.Kind == "method") && len(entry.References) == 0 {
			return true
		}
		for _, name := range entry.References {
			for _, j := range r.byName[name] {
				push(j)
			}
			for _, j := range r.methodsByReceiver[name] {
				push(j)
			}
		}
	}
	return false
}

// evidenceSetCoversKillerDriftContext reports whether prior's evidence covers
// the request under the killer-drift carve-out (REQ-result-stale): the record
// carries complete kill attribution and a compartment ledger; scalar pins are
// equal; the recorded oracle identity set is a subset of the current one — a
// removed identity stays the general rule's domain, while an added identity
// composes: it has no recorded evidence, joins every re-measure's oracle, and
// by the growth keystone cannot un-kill a standing kill; when the set grew,
// the grown-set non-explicit rule binds on both sides (a grown set is a
// derived-oracle claim — an explicit request that supersets the recorded set
// is the caller's selection). Candidate evidence composes rather than
// disqualifying: the
// flagged candidates join the re-measure set downstream under the
// candidate-splice discipline. The compartment delta is attributable; and the
// target's evidence checks plainly valid with its compartment pin refreshed
// to the current one — the refresh licensed by the attributable delta, whose
// every movement the per-oracle walk accounts for. Each retained oracle then
// classifies moved or unmoved: moved when its own evidence no longer checks
// plainly valid (target-package subjects checked with the same refresh, any
// other subject as recorded — its own package's compartment pin is untouched
// by this target's delta) or when its reference walk over the current ledger
// reaches a delta declaration. Returns the moved and added oracle symbols,
// each sorted.
func evidenceSetCoversKillerDriftContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, timeoutDerived bool, memoryPin int64, regime string) (moved, added []string, drifts bool, err error) {
	if prior.CompartmentLedger == nil || prior.OracleExplicit != oracleExplicit ||
		prior.OperatorSet != operatorSet || !timeoutPinMatches(prior, timeout, timeoutDerived) ||
		memoryPinStale(prior, memoryPin) || prior.PropertyRegime != regime ||
		len(prior.OracleEvidence) > len(oracle) ||
		len(prior.Kills) != prior.Killed {
		return nil, nil, false, nil
	}
	if len(prior.OracleEvidence) < len(oracle) && oracleExplicit {
		// A grown set serves only as a derived-oracle claim on both sides
		// (the grown-set rule): an explicit request that supersets the
		// recorded set is the caller's selection, never a derived grown
		// set. The head
		// pinned the explicit flags equal, so one operand speaks for both.
		return nil, nil, false, nil
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return nil, nil, false, nil
		}
		bySymbol[evidence.Symbol] = evidence
	}
	for _, kill := range prior.Kills {
		if kill.Killer == TimeoutKiller || strings.HasPrefix(kill.Killer, PackageKillerPrefix) {
			continue
		}
		if _, recorded := bySymbol[kill.Killer]; !recorded {
			// A kill keyed to a killer with no recorded oracle evidence has
			// no drift signal to classify it by: standing it would trust a
			// ghost the walk cannot see (the flattering direction), so the
			// whole target re-measures.
			return nil, nil, false, nil
		}
	}
	seenCurrent := make(map[string]bool, len(oracle))
	for _, subject := range oracle {
		if seenCurrent[subject.symbol] {
			// A duplicated current oracle symbol would let a removal hide
			// behind the repeat in the retained count; refuse.
			return nil, nil, false, nil
		}
		seenCurrent[subject.symbol] = true
	}
	currentLedger, err := target.view.TestVariantLedger(target.subject)
	if err != nil {
		return nil, nil, false, err
	}
	recordedLedger := prior.CompartmentLedger.ledger()
	delta := gofresh.DiffTestVariantLedgers(recordedLedger, currentLedger)
	if !killerDriftAttributable(delta, recordedLedger, currentLedger) {
		return nil, nil, false, nil
	}
	refreshed := func(subject *subjectView, evidence SubjectEvidence) SubjectEvidence {
		if subject.subject.Package == target.subject.Package && subject.fp.TestVariantClosure != "" {
			evidence.TestVariantClosure = subject.fp.TestVariantClosure
		}
		return evidence
	}
	memo := newRuntimeMemo(runtimeinput.CurrentEnvContext)
	ok, err := evidencePairsValid(ctx, []evidencePair{{subject: target, evidence: refreshed(target, prior.TargetEvidence), accept: acceptValidVerdict}}, memo.once)
	if err != nil || !ok {
		return nil, nil, false, err
	}
	reach := newCompartmentReach(currentLedger, delta)
	if reach.unconditionalRootReaches() {
		// An unchanged var initializer, init function, or TestMain reaching
		// the delta runs changed code around every test: no per-oracle
		// partition is sound, so the whole target re-measures.
		return nil, nil, false, nil
	}
	recordedFuncs := make(map[string]bool, len(recordedLedger.Declarations))
	for _, decl := range recordedLedger.Declarations {
		if decl.Kind == "func" {
			recordedFuncs[decl.Name] = true
		}
	}
	retained := 0
	for _, subject := range oracle {
		evidence, recorded := bySymbol[subject.symbol]
		if !recorded {
			// A current oracle without recorded evidence composes as a grown set
			// only when the RECORDED compartment ledger declares no function
			// of its name: a genuinely added test had no prior declaration,
			// while a dropped evidence row's oracle always did — the
			// record's evidence list is not an identity oracle (a dropped
			// row must refuse, never serve the dropped oracle's kills as
			// unmoved). The match is the bare function name across BOTH
			// compartment variants, fail-closed: a same-named declaration
			// in the sibling variant refuses too, because oracle symbols
			// collapse the variants onto one identity and a name-keyed
			// acceptance would be exactly the laundering channel.
			_, fn := splitTestSymbol(subject.symbol)
			if fn == "" || recordedFuncs[fn] {
				return nil, nil, false, nil
			}
			added = append(added, subject.symbol)
			continue
		}
		retained++
		valid, err := evidencePairsValid(ctx, []evidencePair{{subject: subject, evidence: refreshed(subject, evidence), accept: acceptValidVerdict}}, memo.once)
		if err != nil {
			return nil, nil, false, err
		}
		movedHere := !valid
		if !movedHere && subject.subject.Package == target.subject.Package {
			_, fn := splitTestSymbol(subject.symbol)
			reachesDelta, known := reach.reaches(fn)
			if !known {
				return nil, nil, false, nil
			}
			movedHere = reachesDelta
		}
		if movedHere {
			moved = append(moved, subject.symbol)
		}
	}
	if retained != len(prior.OracleEvidence) {
		// A recorded oracle absent from the current set is a removal — the
		// general rule's domain, never drift's.
		return nil, nil, false, nil
	}
	if ok, err := memo.verify(ctx); err != nil || !ok {
		return nil, nil, false, err
	}
	sort.Strings(moved)
	sort.Strings(added)
	return moved, added, true, nil
}

func evidenceSetMatchesContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, timeoutDerived bool, memoryPin int64, regime string) (bool, error) {
	return evidenceSetMatchesContextWithCurrent(ctx, prior, target, oracle, oracleExplicit, operatorSet, timeout, timeoutDerived, memoryPin, regime, runtimeinput.CurrentEnvContext)
}

func evidenceSetMatchesContextWithCurrent(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, timeoutDerived bool, memoryPin int64, regime string, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.OracleExplicit != oracleExplicit || !timeoutPinMatches(prior, timeout, timeoutDerived) ||
		memoryPinStale(prior, memoryPin) || prior.PropertyRegime != regime || len(prior.OracleEvidence) != len(oracle) {
		return false, nil
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return false, nil
		}
		bySymbol[evidence.Symbol] = evidence
	}
	pairs := make([]evidencePair, 0, 1+len(oracle))
	pairs = append(pairs, evidencePair{subject: target, evidence: prior.TargetEvidence, accept: acceptValidVerdict})
	for _, subject := range oracle {
		evidence, ok := bySymbol[subject.symbol]
		if !ok {
			return false, nil
		}
		pairs = append(pairs, evidencePair{subject: subject, evidence: evidence, accept: acceptValidVerdict})
	}
	memo := newRuntimeMemo(current)
	ok, err := evidencePairsValid(ctx, pairs, memo.once)
	if err != nil || !ok {
		return ok, err
	}
	return memo.verify(ctx)
}

// shapedEvidenceMatchesContext is the shaped-target serve check: the
// ordinary pins and every oracle evidence row, with no target pair —
// the shape digest is compared by the caller as the BodyHash pin
// (REQ-target-structural, REQ-target-manual-recipes).
func shapedEvidenceMatchesContext(ctx context.Context, prior Finding, oracle []*subjectView, operatorSet, timeout string, timeoutDerived bool, memoryPin int64, regime string) (bool, error) {
	if prior.OperatorSet != operatorSet || !prior.OracleExplicit || !timeoutPinMatches(prior, timeout, timeoutDerived) ||
		memoryPinStale(prior, memoryPin) || prior.PropertyRegime != regime || len(prior.OracleEvidence) != len(oracle) ||
		len(prior.CandidateEvidence) != 0 {
		return false, nil
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return false, nil
		}
		bySymbol[evidence.Symbol] = evidence
	}
	pairs := make([]evidencePair, 0, len(oracle))
	for _, subject := range oracle {
		evidence, ok := bySymbol[subject.symbol]
		if !ok {
			return false, nil
		}
		pairs = append(pairs, evidencePair{subject: subject, evidence: evidence, accept: acceptValidVerdict})
	}
	memo := newRuntimeMemo(runtimeinput.CurrentEnvContext)
	ok, err := evidencePairsValid(ctx, pairs, memo.once)
	if err != nil || !ok {
		return ok, err
	}
	return memo.verify(ctx)
}

// ErrEvidenceFinalization marks the evidence-conversion error class:
// the relative conversion revalidating a union against disk inside
// the attach and splice seams. Only this class refuses
// target-locally — input motion between execution and finalization is
// one target's drift — while every other assembly failure (a merge
// conflict, corrupted orchestration state) keeps its campaign-fatal
// signal (REQ-exec-attribution's abort reservation).
var errEvidenceFinalization = errors.New("runtime evidence could not be finalized")

// portableUnion is the persist-boundary form of one merged
// completed-observation union: the absolute observation stays the
// in-memory and merge form (REQ-inputs-absolute-identities), and each
// subject's evidence persists the relative conversion against the base
// its evidence is anchored at — the tree root — so a persisted record
// is keyed by what was measured, not by the checkout root that measured
// it (REQ-inputs-relative-identities). Conversions are memoized per
// base; the env is the oracle-evidence env
// the union's state was computed under, so revalidation inside the
// conversion sees the same environment.
type portableUnion struct {
	absolute runtimeinput.Observation
	env      []string
	byModule map[string]runtimeinput.Observation
}

func newPortableUnion(observation runtimeinput.Observation, evidenceEnv []string) *portableUnion {
	return &portableUnion{absolute: observation, env: evidenceEnv, byModule: map[string]runtimeinput.Observation{}}
}

func (u *portableUnion) at(moduleDir string) (runtimeinput.Observation, error) {
	if obs, ok := u.byModule[moduleDir]; ok {
		return obs, nil
	}
	obs, err := runtimeinput.RelativeEnv(u.absolute, moduleDir, u.env)
	if err != nil {
		return runtimeinput.Observation{}, err
	}
	u.byModule[moduleDir] = obs
	return obs, nil
}

// attachOracleEvidence attaches the completed-observation union to the
// oracle views alone: the shaped-finding form, whose target pair does
// not exist (REQ-target-structural).
func attachOracleEvidence(oracle []*subjectView, union *portableUnion) ([]SubjectEvidence, error) {
	oracleEvidence := make([]SubjectEvidence, 0, len(oracle))
	for _, subject := range oracle {
		evidence, err := attachSubjectEvidence(subject, union)
		if err != nil {
			return nil, err
		}
		oracleEvidence = append(oracleEvidence, evidence)
	}
	sort.Slice(oracleEvidence, func(i, j int) bool { return oracleEvidence[i].Symbol < oracleEvidence[j].Symbol })
	return oracleEvidence, nil
}

// attachSubjectEvidence stamps one subject's evidence from the union's
// portable form at that subject's module.
func attachSubjectEvidence(subject *subjectView, union *portableUnion) (SubjectEvidence, error) {
	observation, err := union.at(subject.evidenceDir)
	if err != nil {
		return SubjectEvidence{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
	}
	state, err := runtimeinput.CompletedState(observation)
	if err != nil {
		return SubjectEvidence{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
	}
	fp, err := subject.view.AttachObservation(subject.subject, subject.fp, observation)
	if err != nil {
		return SubjectEvidence{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
	}
	return evidenceFromFingerprint(subject.symbol, fp, state), nil
}

func attachEvidence(target *subjectView, oracle []*subjectView, union *portableUnion) (SubjectEvidence, []SubjectEvidence, error) {
	targetEvidence, err := attachSubjectEvidence(target, union)
	if err != nil {
		return SubjectEvidence{}, nil, err
	}
	oracleEvidence, err := attachOracleEvidence(oracle, union)
	if err != nil {
		return SubjectEvidence{}, nil, err
	}
	return targetEvidence, oracleEvidence, nil
}
