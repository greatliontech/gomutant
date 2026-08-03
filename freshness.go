package gomutant

import (
	"context"
	"fmt"
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

type subjectView struct {
	symbol      string
	subject     gofresh.Subject
	moduleDir   string
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
	env      []string
	progress func(phase, pkg string)
	byDir    map[string]*gofresh.Engine
}

func (t *Tree) newSubjectEngines(progress func(phase, pkg string)) *subjectEngines {
	return &subjectEngines{env: t.eng.GoEnv(), progress: progress, byDir: map[string]*gofresh.Engine{}}
}

func (e *subjectEngines) engineFor(dir string) (*gofresh.Engine, error) {
	if engine, ok := e.byDir[dir]; ok {
		return engine, nil
	}
	opts := []gofresh.Option{gofresh.WithDir(dir), gofresh.WithEnv(e.env...)}
	if e.progress != nil {
		progress := e.progress
		opts = append(opts, gofresh.WithProgress(func(p gofresh.Progress) { progress(p.Phase, p.Package) }))
	}
	engine, err := gofresh.New(opts...)
	if err != nil {
		return nil, err
	}
	e.byDir[dir] = engine
	return engine, nil
}

func (t *Tree) newSubjectViews(ctx context.Context, symbols []string) (*subjectViewSet, error) {
	return t.newSubjectViewsWithPackageContext(ctx, symbols, t.eng.PackageContextContext, false, t.newSubjectEngines(nil))
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
	env := engines.env
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
				env: env, view: view, fp: fp, sourceFiles: sourceFiles, module: module,
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
	env := engines.env
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
				env: env, view: view, fp: captured, sourceFiles: sourceFiles, module: module,
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
				symbol: sv.symbol, subject: sv.subject, moduleDir: sv.moduleDir,
				env: sv.env, view: sibling, fp: sv.fp, sourceFiles: sv.sourceFiles, module: siblingModule,
			}
		}
	}
	return narrowed, nil
}

func (t *Tree) newSubjectView(symbol string) (*subjectView, error) {
	views, err := t.newSubjectViews(context.Background(), []string{symbol})
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
	declared, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return FindingInspection{}, err
	}
	i := sort.SearchStrings(declared, f.Symbol)
	if i == len(declared) || declared[i] != f.Symbol {
		return FindingInspection{State: FindingDetached, Reason: "mutated symbol no longer resolves"}, nil
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
		supplementary, err := t.newSubjectViews(ctx, missing)
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

func sortedSubjectEvidence(evidence []SubjectEvidence) []SubjectEvidence {
	sorted := append([]SubjectEvidence(nil), evidence...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Symbol < sorted[j].Symbol })
	return sorted
}

func sameAttestationPins(prior, current Finding) bool {
	if prior.OperatorSet != current.OperatorSet || prior.OracleExplicit != current.OracleExplicit || prior.Budget != current.Budget ||
		prior.CandidateCount != current.CandidateCount || prior.Generated != current.Generated ||
		prior.OracleTimeout != current.OracleTimeout || prior.TargetEvidence != current.TargetEvidence ||
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
		if priorEvidence, ok := bySymbol[evidence.Symbol]; !ok || priorEvidence != evidence {
			return false
		}
	}
	return true
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
		parts = append(parts, "added: "+strings.Join(added, ", "))
	}
	if len(removed) != 0 {
		parts = append(parts, "removed: "+strings.Join(removed, ", "))
	}
	return "derived oracle changed (" + strings.Join(parts, "; ") + ")"
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
func evidenceSetCoversGrowthContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string) ([]string, bool, error) {
	// Growth is a derived-oracle claim on both sides: an explicit request
	// that happens to superset the recorded derived set is the caller's
	// selection, not derived growth — serving it would report "derived
	// oracle grew" for a set nothing derived, and persist an explicit
	// oracle under a non-explicit record.
	if oracleExplicit || prior.OracleExplicit || prior.CompartmentLedger == nil ||
		prior.OperatorSet != operatorSet || prior.OracleTimeout != timeout ||
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

func evidenceSetMatchesContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string) (bool, error) {
	return evidenceSetMatchesContextWithCurrent(ctx, prior, target, oracle, oracleExplicit, operatorSet, timeout, runtimeinput.CurrentEnvContext)
}

func evidenceSetMatchesContextWithCurrent(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.OracleExplicit != oracleExplicit || prior.OracleTimeout != timeout || len(prior.OracleEvidence) != len(oracle) {
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

func evidenceSetMatches(prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string) (bool, error) {
	return evidenceSetMatchesContext(context.Background(), prior, target, oracle, oracleExplicit, operatorSet, timeout)
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
		return evidenceFromFingerprint(subject.symbol, fp, state), nil
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
