package gomutant

import (
	"context"
	"fmt"
	"path/filepath"
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
	// moduleBase is moduleDir relative to the tree root in slash form
	// ("" for the root module): the machine-portable base persisted on
	// evidence so the store's portable-line walk resolves each subject's
	// manifest against that subject's own module (REQ-result-layers).
	moduleBase  string
	env         []string
	view        *gofresh.View
	fp          gofresh.Fingerprint
	sourceFiles []string
	module      *moduleSubjectView
}

// treeRelModuleBase computes a subject module's tree-relative slash
// base: "" for the root module, and "" fail-safe when the module
// escapes the tree - the walk then falls back to the tree root, the
// pre-base behavior.
func treeRelModuleBase(treeDir, moduleDir string) string {
	rel, err := filepath.Rel(treeDir, moduleDir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return filepath.ToSlash(rel)
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
	byDir          map[string]*gofresh.Engine
}

func (t *Tree) newSubjectEngines(event func(phase, pkg, detail string), packageProcess bool) *subjectEngines {
	env := t.eng.GoEnv()
	return &subjectEngines{env: env, evidenceEnv: engine.OracleEvidenceEnv(env), vouches: t.vouches, event: event, packageProcess: packageProcess, byDir: map[string]*gofresh.Engine{}}
}

func (e *subjectEngines) engineFor(dir string) (*gofresh.Engine, error) {
	if engine, ok := e.byDir[dir]; ok {
		return engine, nil
	}
	opts := []gofresh.Option{gofresh.WithDir(dir), gofresh.WithEnv(e.env...), gofresh.WithProducerEnv(e.evidenceEnv...)}
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
// spelling and keeps the first-dot cut.
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

func (t *Tree) newSubjectViews(ctx context.Context, symbols []string, packageProcess bool) (*subjectViewSet, error) {
	return t.newSubjectViewsWithPackageContext(ctx, symbols, t.eng.PackageContextContext, false, t.newSubjectEngines(nil, packageProcess))
}

// newSubjectViewsFaultTolerant builds the decision-evidence view set
// with per-symbol fault routing: a symbol that fails to resolve, or a
// module group whose engine, view, or capture fails, records the fault
// for each affected symbol instead of aborting the set - the same
// target-locality the observed union carries (REQ-exec-quiescence);
// only the run's own cancellation aborts.
func (t *Tree) newSubjectViewsFaultTolerant(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), engines *subjectEngines) (*subjectViewSet, map[string]error, error) {
	faults := map[string]error{}
	groups, err := t.resolveModuleGroups(ctx, symbols, packageContext, func(symbol string, err error) error {
		faults[symbol] = err
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	set := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(symbols))}
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
				symbol: r.symbol, subject: r.subject, moduleDir: r.moduleDir,
				moduleBase: treeRelModuleBase(t.dir, r.moduleDir),
				env:        env, view: view, fp: fp, sourceFiles: sourceFiles, module: module,
			}
		}
		return nil
	}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
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
				return nil, nil, err
			}
			continue
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
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
					return nil, nil, ctx.Err()
				}
				for _, r := range subset {
					faults[r.symbol] = subErr
				}
				continue
			}
			subModule := &moduleSubjectView{view: subView, validate: subView.Validate}
			set.modules = append(set.modules, subModule)
			if err := capture(ctx, subView, subModule, subset); err != nil {
				return nil, nil, err
			}
		}
	}
	return set, faults, nil
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

func (t *Tree) newSubjectViewsWithPackageContext(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), observed bool, engines *subjectEngines) (*subjectViewSet, error) {
	groups, err := t.resolveModuleGroups(ctx, symbols, packageContext, func(_ string, err error) error { return err })
	if err != nil {
		return nil, err
	}
	set := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(symbols))}
	env := engines.evidenceEnv
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		engine, err := engines.engineFor(group.dir)
		if err != nil {
			return nil, err
		}
		view, err := engine.NewViewFor(ctx, group.subjects, group.dir, gofresh.CodeResult)
		if err != nil {
			return nil, err
		}
		// One Validate covers every capture class: the view revalidates
		// whatever it captured (the collapsed evidence-tier surface).
		module := &moduleSubjectView{view: view, validate: view.Validate}
		var observedFingerprints map[gofresh.Subject]gofresh.Fingerprint
		if observed {
			// One batched proof pass per view: the observability analysis is
			// shared across the view's whole subject set instead of re-run per
			// subject, with per-subject fingerprints read from the batch.
			observedFingerprints, err = view.CaptureObservedBatch(ctx)
			if err != nil {
				return nil, err
			}
		}
		set.modules = append(set.modules, module)
		for _, resolved := range group.resolved {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var fp gofresh.Fingerprint
			if observed {
				captured, ok := observedFingerprints[resolved.subject]
				if !ok {
					return nil, fmt.Errorf("gomutant: batched observation capture omitted subject %s.%s", resolved.subject.Package, resolved.subject.Symbol)
				}
				fp = captured
			} else {
				fp, err = view.Capture(ctx, resolved.subject)
				if err != nil {
					return nil, err
				}
			}
			sourceFiles, err := view.SourceFilesFor(resolved.subject)
			if err != nil {
				return nil, err
			}
			set.bySymbol[resolved.symbol] = &subjectView{
				symbol: resolved.symbol, subject: resolved.subject, moduleDir: resolved.moduleDir,
				moduleBase: treeRelModuleBase(t.dir, resolved.moduleDir),
				env:        env, view: view, fp: fp, sourceFiles: sourceFiles, module: module,
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return set, nil
}

// newObservedUnionViews builds one observed view set over every symbol,
// tolerating per-symbol faults: a symbol that fails to resolve, or whose
// module group's view or batched proof pass fails, lands in the fault
// map instead of failing the union — evidence-construction failures stay
// target-local (REQ-exec-quiescence), and one shared observation pass
// replaces the per-target passes the campaign previously paid. Only the
// campaign's own cancellation aborts.
func (t *Tree) newObservedUnionViews(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), engines *subjectEngines) (*subjectViewSet, map[string]error, error) {
	faults := map[string]error{}
	groups, err := t.resolveModuleGroups(ctx, symbols, packageContext, func(symbol string, err error) error {
		faults[symbol] = err
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	set := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(symbols))}
	env := engines.evidenceEnv
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		groupFault := func(err error) {
			for _, resolved := range group.resolved {
				faults[resolved.symbol] = err
			}
		}
		engine, err := engines.engineFor(group.dir)
		if err != nil {
			groupFault(err)
			continue
		}
		view, err := engine.NewViewFor(ctx, group.subjects, group.dir, gofresh.CodeResult)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			groupFault(err)
			continue
		}
		// The union's parent views are never validated directly — no
		// reader consumes the union's modules list. Each measured target
		// validates its own sibling narrowing, which re-observes the
		// same facts, so the parents are covered transitively; a module
		// with no measured target stays unvalidated exactly as a skipped
		// target's module does.
		module := &moduleSubjectView{view: view, validate: view.Validate}
		observedFingerprints, err := view.CaptureObservedBatch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			groupFault(err)
			continue
		}
		for _, resolved := range group.resolved {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			captured, ok := observedFingerprints[resolved.subject]
			if !ok {
				faults[resolved.symbol] = fmt.Errorf("gomutant: batched observation capture omitted subject %s.%s", resolved.subject.Package, resolved.subject.Symbol)
				continue
			}
			sourceFiles, err := view.SourceFilesFor(resolved.subject)
			if err != nil {
				faults[resolved.symbol] = err
				continue
			}
			set.bySymbol[resolved.symbol] = &subjectView{
				symbol: resolved.symbol, subject: resolved.subject, moduleDir: resolved.moduleDir,
				moduleBase: treeRelModuleBase(t.dir, resolved.moduleDir),
				env:        env, view: view, fp: captured, sourceFiles: sourceFiles, module: module,
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return set, faults, nil
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
func (s *subjectViewSet) forTarget(target string, oracle []string, faults map[string]error) (*subjectViewSet, error) {
	narrowed := &subjectViewSet{bySymbol: make(map[string]*subjectView, 1+len(oracle))}
	type siblingGroup struct {
		views    []*subjectView
		subjects []gofresh.Subject
	}
	var order []*moduleSubjectView
	groups := map[*moduleSubjectView]*siblingGroup{}
	// narrowed.bySymbol is populated only from sibling derivations below:
	// a raw union-backed entry would share the union's attach-once state
	// and seal — the exact collision this narrowing exists to prevent.
	seen := map[string]bool{}
	for _, symbol := range append([]string{target}, oracle...) {
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		sv, ok := s.bySymbol[symbol]
		if !ok {
			if err, faulted := faults[symbol]; faulted {
				return nil, err
			}
			return nil, fmt.Errorf("union view set carries no subject %s", symbol)
		}
		group, ok := groups[sv.module]
		if !ok {
			group = &siblingGroup{}
			groups[sv.module] = group
			order = append(order, sv.module)
		}
		group.views = append(group.views, sv)
		group.subjects = append(group.subjects, sv.subject)
	}
	for _, module := range order {
		group := groups[module]
		sibling, err := module.view.Sibling(group.subjects)
		if err != nil {
			return nil, err
		}
		siblingModule := &moduleSubjectView{view: sibling, validate: sibling.Validate}
		narrowed.modules = append(narrowed.modules, siblingModule)
		for _, sv := range group.views {
			narrowed.bySymbol[sv.symbol] = &subjectView{
				symbol: sv.symbol, subject: sv.subject, moduleDir: sv.moduleDir, moduleBase: sv.moduleBase,
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
// gofresh verdict lets a recorded subject stand. The oracle-growth gate
// reaches the same bar by refreshing a target-package subject's compartment
// pin before checking — the refresh its inert ledger diff licenses
// (REQ-result-stale's growth carve-out) — never by tolerating a stale
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
	state, err := current(ctx, evidence.RuntimeInputs, s.moduleDir, s.env)
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
	state, err := runtimeinput.CurrentEnvContext(ctx, evidence.RuntimeInputs, s.moduleDir, s.env)
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
		return FindingInspection{State: FindingStale, Reason: "runtime inputs changed" + movedInputSuffix(ctx, evidence.RuntimeInputs, s.moduleDir, s.env)}, nil
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

// InspectFinding classifies a parsed finding against the current tree without
// running tests (REQ-result-inspection).
func (t *Tree) InspectFinding(f Finding) (FindingInspection, error) {
	return t.InspectFindingContext(context.Background(), f)
}

// InspectFindingContext is InspectFinding with caller-owned cancellation.
func (t *Tree) InspectFindingContext(ctx context.Context, f Finding) (FindingInspection, error) {
	inspection, err := t.inspectFindingStateContext(ctx, f, nil)
	if err != nil {
		return FindingInspection{}, err
	}
	inspection.CandidateEvidence = canonicalCandidateEvidence(f.CandidateEvidence)
	// The state answers "can this record be reused as it stands"; flagged
	// candidates mean it cannot — they re-execute before any serve — so a
	// record otherwise current classifies unverifiable with the candidate
	// evidence carrying the specifics (REQ-result-inspection).
	if inspection.State == FindingCurrent && len(inspection.CandidateEvidence) != 0 {
		inspection.State = FindingUnverifiable
		inspection.Reason = fmt.Sprintf("%d candidate(s) carry unverifiable runtime evidence and re-execute before reuse", len(inspection.CandidateEvidence))
	}
	return inspection, nil
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
	if f.Shape != nil {
		return t.inspectShapedFindingContext(ctx, f)
	}
	declared, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return FindingInspection{}, err
	}
	i := sort.SearchStrings(declared, f.Symbol)
	if i == len(declared) || declared[i] != f.Symbol {
		return FindingInspection{State: FindingDetached, Reason: "mutated symbol no longer resolves - terminal: no re-measure can revive this record; prune removes it, retarget follows a rename"}, nil
	}
	if f.OperatorSet != engine.OperatorSet {
		return FindingInspection{State: FindingStale, Reason: "operator set changed"}, nil
	}
	if _, err := time.ParseDuration(f.OracleTimeout); err != nil {
		return FindingInspection{}, fmt.Errorf("finding %s has invalid oracle timeout: %w", f.Symbol, err)
	}
	if !f.OracleExplicit {
		currentOracle, err := t.resolveOracleContext(ctx, Target{Symbol: f.Symbol})
		if err != nil {
			return FindingInspection{}, err
		}
		recordedOracle := make([]string, len(f.OracleEvidence))
		for i, evidence := range f.OracleEvidence {
			recordedOracle[i] = evidence.Symbol
		}
		sort.Strings(recordedOracle)
		if reason := derivedOracleDelta(recordedOracle, currentOracle); reason != "" {
			// The identity delta alone under-describes the edit that
			// staled the record: an oracle test STRENGTHENED IN PLACE
			// beside additions is the one that matters most to a caller
			// who just wrote kill-tests, and stopping at "added: ..."
			// hides it (the pando field report). Name the surviving
			// oracle tests whose own evidence moved WHILE THE TARGET'S
			// STANDS - a moved target moves every oracle's closure with
			// it, so a list there would point at untouched tests -
			// stale-only, best-effort: the stale path re-measures
			// anyway, so one evidence walk is cheap against it.
			if modified := t.modifiedOracleNames(ctx, f, currentOracle, prebuilt); len(modified) > 0 {
				reason = strings.TrimSuffix(reason, ")") + "; modified: " + cappedNameList(modified, "tests") + ")"
			}
			return FindingInspection{State: FindingStale, Reason: reason}, nil
		}
	}
	oracle := sortedSubjectEvidence(f.OracleEvidence)
	validOracle := make(map[string]bool, len(oracle))
	symbols := []string{f.Symbol}
	for _, evidence := range oracle {
		if err := t.eng.ValidateOracleContext(ctx, []string{evidence.Symbol}); err == nil {
			validOracle[evidence.Symbol] = true
			symbols = append(symbols, evidence.Symbol)
		}
	}
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
		supplementary, err := t.newSubjectViews(ctx, missing, findingPackageProcessAttestable(f))
		if err != nil {
			return FindingInspection{}, err
		}
		for symbol, view := range supplementary.bySymbol {
			viewFor[symbol] = view
		}
	}
	target := viewFor[f.Symbol]
	inspection, err := target.inspectContext(ctx, f.TargetEvidence)
	if err != nil || inspection.State != FindingCurrent {
		// The reason names its responsible subject so it stays
		// self-contained when copied out of the record's context,
		// parallel to the oracle prefix below (REQ-result-inspection).
		if err == nil && inspection.Reason != "" {
			inspection.Reason = "target: " + inspection.Reason
		}
		return inspection, err
	}
	for _, evidence := range oracle {
		if err := ctx.Err(); err != nil {
			return FindingInspection{}, err
		}
		if !validOracle[evidence.Symbol] {
			return FindingInspection{State: FindingStale, Reason: "oracle " + evidence.Symbol + " no longer resolves"}, nil
		}
		view := viewFor[evidence.Symbol]
		inspection, err := view.inspectContext(ctx, evidence)
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

// inspectShapedFindingContext derives a shaped finding's state: the
// declared shape re-derives against the current tree (a moved digest is
// stale, a shape that no longer resolves — a departed scoped package,
// type, or recipe file — is stale with the deriving refusal named,
// never detached: retirement is the caller's explicit edit), and the
// oracle evidence rows inspect exactly as a symbol finding's do
// (REQ-target-structural, REQ-target-manual-recipes,
// REQ-result-inspection).
func (t *Tree) inspectShapedFindingContext(ctx context.Context, f Finding) (FindingInspection, error) {
	if f.OperatorSet != shapedOperatorSet {
		return FindingInspection{State: FindingStale, Reason: "shaped operator set changed"}, nil
	}
	if _, err := time.ParseDuration(f.OracleTimeout); err != nil {
		return FindingInspection{}, fmt.Errorf("finding %s has invalid oracle timeout: %w", f.Symbol, err)
	}
	_, digest, err := t.shapedCandidates(ctx, Target{Symbol: f.Symbol, Structural: f.Shape.Structural, Manual: f.Shape.Manual, Oracle: []string{"-"}, OracleExplicit: true})
	if err != nil {
		if ctx.Err() != nil {
			return FindingInspection{}, ctx.Err()
		}
		return FindingInspection{State: FindingStale, Reason: "shape no longer derives: " + err.Error()}, nil
	}
	if digest != f.BodyHash {
		return FindingInspection{State: FindingStale, Reason: "the declared shape or a probed file moved"}, nil
	}
	oracle := sortedSubjectEvidence(f.OracleEvidence)
	var symbols []string
	validOracle := make(map[string]bool, len(oracle))
	for _, evidence := range oracle {
		if err := t.eng.ValidateOracleContext(ctx, []string{evidence.Symbol}); err == nil {
			validOracle[evidence.Symbol] = true
			symbols = append(symbols, evidence.Symbol)
		}
	}
	viewFor := make(map[string]*subjectView, len(symbols))
	if len(symbols) > 0 {
		supplementary, err := t.newSubjectViews(ctx, symbols, findingPackageProcessAttestable(f))
		if err != nil {
			return FindingInspection{}, err
		}
		for symbol, view := range supplementary.bySymbol {
			viewFor[symbol] = view
		}
	}
	for _, evidence := range oracle {
		if err := ctx.Err(); err != nil {
			return FindingInspection{}, err
		}
		if !validOracle[evidence.Symbol] {
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

func sameAttestationPins(prior, current Finding) bool {
	if prior.PropertyRegime != current.PropertyRegime {
		return false
	}
	if prior.OperatorSet != current.OperatorSet || prior.OracleExplicit != current.OracleExplicit || prior.Budget != current.Budget ||
		prior.CandidateCount != current.CandidateCount || prior.Generated != current.Generated ||
		prior.OracleTimeout != current.OracleTimeout || prior.OracleMemoryBytes != current.OracleMemoryBytes || attestationPinView(prior.TargetEvidence) != attestationPinView(current.TargetEvidence) ||
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
	currentSet := make(map[string]bool, len(currentOracle))
	for _, symbol := range currentOracle {
		currentSet[symbol] = true
	}
	// The intersection's function names, mapped back to their symbols:
	// oracle validation already refuses a name declared in both
	// compartment variants, so the name is unambiguous here.
	byName := map[string]string{}
	for _, evidence := range f.OracleEvidence {
		if !currentSet[evidence.Symbol] {
			continue
		}
		if _, fn := splitTestSymbol(evidence.Symbol); fn != "" {
			byName[fn] = evidence.Symbol
		}
	}
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

// evidenceSetCoversGrowthContext reports whether prior's evidence covers the
// request under the oracle-growth carve-out (REQ-result-stale): the finding
// is non-explicit with a recorded compartment ledger; scalar pins equal; the
// current derived oracle is a strict superset of the recorded set; the
// recorded ledger diffs inert against the current view's — the one movement
// the carve-out tolerates — and the target's and every retained oracle's
// evidence checks plainly valid, target-package subjects with their recorded
// compartment pin refreshed to the current one. The refresh is what the
// inert diff licenses; tolerating the stale "test variants" verdict instead
// would let a moved pin hide behind it — gofresh orders the compartment
// comparison after the core and before the environment tiers, so that
// reason certifies core equality only. Returns the added oracle symbols,
// sorted.
func evidenceSetCoversGrowthContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, memoryPin int64, regime string) ([]string, bool, error) {
	// Growth is a derived-oracle claim on both sides: an explicit request
	// that happens to superset the recorded derived set is the caller's
	// selection, not derived growth — serving it would report "derived
	// oracle grew" for a set nothing derived, and persist an explicit
	// oracle under a non-explicit record.
	if oracleExplicit || prior.OracleExplicit || prior.CompartmentLedger == nil ||
		prior.OperatorSet != operatorSet || prior.OracleTimeout != timeout ||
		prior.OracleMemoryBytes != memoryPin || prior.PropertyRegime != regime ||
		len(prior.OracleEvidence) >= len(oracle) || len(prior.CandidateEvidence) != 0 {
		return nil, false, nil
	}
	currentLedger, err := target.view.TestVariantLedger(target.subject)
	if err != nil {
		return nil, false, err
	}
	delta := gofresh.DiffTestVariantLedgers(prior.CompartmentLedger.ledger(), currentLedger)
	if !delta.Inert() {
		return nil, false, nil
	}
	refreshed := func(subject *subjectView, evidence SubjectEvidence) SubjectEvidence {
		if subject.subject.Package == target.subject.Package && subject.fp.TestVariantClosure != "" {
			evidence.TestVariantClosure = subject.fp.TestVariantClosure
		}
		return evidence
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return nil, false, nil
		}
		bySymbol[evidence.Symbol] = evidence
	}
	pairs := make([]evidencePair, 0, 1+len(oracle))
	pairs = append(pairs, evidencePair{subject: target, evidence: refreshed(target, prior.TargetEvidence), accept: acceptValidVerdict})
	var added []string
	retained := 0
	for _, subject := range oracle {
		evidence, recorded := bySymbol[subject.symbol]
		if !recorded {
			added = append(added, subject.symbol)
			continue
		}
		retained++
		// The inert diff explains only the target package's compartment: a
		// target-package subject checks with its compartment pin refreshed,
		// a subject elsewhere with its recorded evidence untouched. Derived
		// oracles are package-local today, so the elsewhere case is
		// fail-closed defense, not a reachable path.
		pairs = append(pairs, evidencePair{subject: subject, evidence: refreshed(subject, evidence), accept: acceptValidVerdict})
	}
	if retained != len(prior.OracleEvidence) || len(added) == 0 {
		// A recorded oracle absent from the current set is a removal, never
		// growth; growth with nothing added is not growth.
		return nil, false, nil
	}
	memo := newRuntimeMemo(runtimeinput.CurrentEnvContext)
	ok, err := evidencePairsValid(ctx, pairs, memo.once)
	if err != nil || !ok {
		return nil, false, err
	}
	if ok, err := memo.verify(ctx); err != nil || !ok {
		return nil, false, err
	}
	sort.Strings(added)
	return added, true, nil
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
// growth's non-explicit rule binds on both sides (growth is a derived-oracle
// claim — an explicit request that supersets the recorded set is the caller's
// selection). Candidate evidence composes rather than disqualifying: the
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
func evidenceSetCoversKillerDriftContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, memoryPin int64, regime string) (moved, added []string, drifts bool, err error) {
	if prior.CompartmentLedger == nil || prior.OracleExplicit != oracleExplicit ||
		prior.OperatorSet != operatorSet || prior.OracleTimeout != timeout ||
		prior.OracleMemoryBytes != memoryPin || prior.PropertyRegime != regime ||
		len(prior.OracleEvidence) > len(oracle) ||
		len(prior.Kills) != prior.Killed {
		return nil, nil, false, nil
	}
	if len(prior.OracleEvidence) < len(oracle) && oracleExplicit {
		// A grown set serves only as a derived-oracle claim on both sides
		// (growth's rule): an explicit request that supersets the recorded
		// set is the caller's selection, never derived growth. The head
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
			// A current oracle without recorded evidence composes as growth
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

func evidenceSetMatchesContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, memoryPin int64, regime string) (bool, error) {
	return evidenceSetMatchesContextWithCurrent(ctx, prior, target, oracle, oracleExplicit, operatorSet, timeout, memoryPin, regime, runtimeinput.CurrentEnvContext)
}

func evidenceSetMatchesContextWithCurrent(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, memoryPin int64, regime string, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.OracleExplicit != oracleExplicit || prior.OracleTimeout != timeout ||
		prior.OracleMemoryBytes != memoryPin || prior.PropertyRegime != regime || len(prior.OracleEvidence) != len(oracle) {
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
func shapedEvidenceMatchesContext(ctx context.Context, prior Finding, oracle []*subjectView, operatorSet, timeout string, memoryPin int64, regime string) (bool, error) {
	if prior.OperatorSet != operatorSet || !prior.OracleExplicit || prior.OracleTimeout != timeout ||
		prior.OracleMemoryBytes != memoryPin || prior.PropertyRegime != regime || len(prior.OracleEvidence) != len(oracle) ||
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

func evidenceSetMatches(prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, memoryPin int64, regime string) (bool, error) {
	return evidenceSetMatchesContext(context.Background(), prior, target, oracle, oracleExplicit, operatorSet, timeout, memoryPin, regime)
}

// attachOracleEvidence attaches the completed-observation union to the
// oracle views alone: the shaped-finding form, whose target pair does
// not exist (REQ-target-structural).
func attachOracleEvidence(oracle []*subjectView, observation runtimeinput.Observation) ([]SubjectEvidence, error) {
	state, err := runtimeinput.CompletedState(observation)
	if err != nil {
		return nil, err
	}
	oracleEvidence := make([]SubjectEvidence, 0, len(oracle))
	for _, subject := range oracle {
		fp, err := subject.view.AttachObservation(subject.subject, subject.fp, observation)
		if err != nil {
			return nil, err
		}
		evidence := evidenceFromFingerprint(subject.symbol, fp, state)
		evidence.ModuleBase = subject.moduleBase
		oracleEvidence = append(oracleEvidence, evidence)
	}
	sort.Slice(oracleEvidence, func(i, j int) bool { return oracleEvidence[i].Symbol < oracleEvidence[j].Symbol })
	return oracleEvidence, nil
}

func attachEvidence(target *subjectView, oracle []*subjectView, observation runtimeinput.Observation) (SubjectEvidence, []SubjectEvidence, error) {
	state, err := runtimeinput.CompletedState(observation)
	if err != nil {
		return SubjectEvidence{}, nil, err
	}
	attach := func(subject *subjectView) (SubjectEvidence, error) {
		fp, err := subject.view.AttachObservation(subject.subject, subject.fp, observation)
		if err != nil {
			return SubjectEvidence{}, err
		}
		evidence := evidenceFromFingerprint(subject.symbol, fp, state)
		evidence.ModuleBase = subject.moduleBase
		return evidence, nil
	}
	targetEvidence, err := attach(target)
	if err != nil {
		return SubjectEvidence{}, nil, err
	}
	oracleEvidence := make([]SubjectEvidence, 0, len(oracle))
	for _, subject := range oracle {
		evidence, err := attach(subject)
		if err != nil {
			return SubjectEvidence{}, nil, err
		}
		oracleEvidence = append(oracleEvidence, evidence)
	}
	sort.Slice(oracleEvidence, func(i, j int) bool { return oracleEvidence[i].Symbol < oracleEvidence[j].Symbol })
	return targetEvidence, oracleEvidence, nil
}
