package gomutant

import (
	"context"
	"fmt"
	"sort"
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

func (t *Tree) newSubjectViewsWithPackageContext(ctx context.Context, symbols []string, packageContext func(context.Context, string) (string, string, error), observed bool, engines *subjectEngines) (*subjectViewSet, error) {
	type resolvedSubject struct {
		symbol, moduleDir string
		subject           gofresh.Subject
	}
	type moduleGroup struct {
		dir      string
		resolved []resolvedSubject
		subjects []gofresh.Subject
	}
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
			return nil, err
		}
		if pkg == "" || local == "" {
			return nil, fmt.Errorf("subject %s does not resolve", symbol)
		}
		moduleDir, _, err := packageContext(ctx, pkg)
		if err != nil {
			return nil, err
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
	set := &subjectViewSet{bySymbol: make(map[string]*subjectView, len(seen))}
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

func (s *subjectView) valid(evidence SubjectEvidence) (bool, error) {
	return s.validContext(context.Background(), evidence)
}

func (s *subjectView) validContext(ctx context.Context, evidence SubjectEvidence) (bool, error) {
	return s.validContextWithCurrent(ctx, evidence, runtimeinput.CurrentEnvContext)
}

func (s *subjectView) validContextWithCurrent(ctx context.Context, evidence SubjectEvidence, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
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
	verdict, err := s.checkContext(ctx, evidence.fingerprint())
	if err != nil {
		return false, err
	}
	return verdict.Status == gofresh.Valid, nil
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
		return FindingInspection{State: FindingStale, Reason: "runtime inputs changed"}, nil
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

func (s *subjectView) checkContext(ctx context.Context, fingerprint gofresh.Fingerprint) (gofresh.Verdict, error) {
	if fingerprint.ObservationAssertion != "" || fingerprint.ObservationProof != (gofresh.ObservationProof{}) {
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
	inspection, err := t.inspectFindingStateContext(ctx, f)
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

func (t *Tree) inspectFindingStateContext(ctx context.Context, f Finding) (FindingInspection, error) {
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
		if len(currentOracle) != len(recordedOracle) {
			return FindingInspection{State: FindingStale, Reason: "derived oracle changed"}, nil
		}
		for i := range currentOracle {
			if currentOracle[i] != recordedOracle[i] {
				return FindingInspection{State: FindingStale, Reason: "derived oracle changed"}, nil
			}
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
	views, err := t.newSubjectViews(ctx, symbols)
	if err != nil {
		return FindingInspection{}, err
	}
	target := views.bySymbol[f.Symbol]
	inspection, err := target.inspectContext(ctx, f.TargetEvidence)
	if err != nil || inspection.State != FindingCurrent {
		return inspection, err
	}
	for _, evidence := range oracle {
		if err := ctx.Err(); err != nil {
			return FindingInspection{}, err
		}
		if !validOracle[evidence.Symbol] {
			return FindingInspection{State: FindingStale, Reason: "oracle " + evidence.Symbol + " no longer resolves"}, nil
		}
		view := views.bySymbol[evidence.Symbol]
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

func evidenceSetMatchesContext(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string) (bool, error) {
	return evidenceSetMatchesContextWithCurrent(ctx, prior, target, oracle, oracleExplicit, operatorSet, timeout, runtimeinput.CurrentEnvContext)
}

func evidenceSetMatchesContextWithCurrent(ctx context.Context, prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string, current func(context.Context, string, string, []string) (runtimeinput.State, error)) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.OracleExplicit != oracleExplicit || prior.OracleTimeout != timeout || len(prior.OracleEvidence) != len(oracle) {
		return false, nil
	}
	type runtimeKey struct {
		manifest, moduleDir, environment string
	}
	type runtimeResult struct {
		state runtimeinput.State
		err   error
		env   []string
		uses  int
	}
	runtimeResults := map[runtimeKey]*runtimeResult{}
	var runtimeOrder []runtimeKey
	currentOnce := func(ctx context.Context, manifest, moduleDir string, env []string) (runtimeinput.State, error) {
		key := runtimeKey{manifest: manifest, moduleDir: moduleDir, environment: sequenceKey(env)}
		if result, ok := runtimeResults[key]; ok {
			result.uses++
			return result.state, result.err
		}
		state, err := current(ctx, manifest, moduleDir, env)
		runtimeResults[key] = &runtimeResult{state: state, err: err, env: append([]string(nil), env...), uses: 1}
		runtimeOrder = append(runtimeOrder, key)
		return state, err
	}
	ok, err := target.validContextWithCurrent(ctx, prior.TargetEvidence, currentOnce)
	if err != nil || !ok {
		return ok, err
	}
	bySymbol := make(map[string]SubjectEvidence, len(prior.OracleEvidence))
	for _, evidence := range prior.OracleEvidence {
		if _, duplicate := bySymbol[evidence.Symbol]; duplicate {
			return false, nil
		}
		bySymbol[evidence.Symbol] = evidence
	}
	for _, subject := range oracle {
		evidence, ok := bySymbol[subject.symbol]
		if !ok {
			return false, nil
		}
		valid, err := subject.validContextWithCurrent(ctx, evidence, currentOnce)
		if err != nil || !valid {
			return valid, err
		}
	}
	for _, key := range runtimeOrder {
		result := runtimeResults[key]
		if result.uses < 2 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		state, err := current(ctx, key.manifest, key.moduleDir, result.env)
		if err != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil || state != result.state {
			return false, nil
		}
	}
	return true, nil
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
