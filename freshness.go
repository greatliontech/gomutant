package gomutant

import (
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
type FindingInspection struct {
	State  FindingState `json:"state"`
	Reason string       `json:"reason,omitempty"`
}

type subjectView struct {
	symbol    string
	subject   gofresh.Subject
	moduleDir string
	env       []string
	view      *gofresh.View
	fp        gofresh.Fingerprint
}

func (t *Tree) newSubjectView(symbol string) (*subjectView, error) {
	pkg, local := t.eng.PackageOf(symbol)
	if pkg == "" || local == "" {
		return nil, fmt.Errorf("subject %s does not resolve", symbol)
	}
	moduleDir, _, err := t.eng.PackageContext(pkg)
	if err != nil {
		return nil, err
	}
	env := t.eng.GoEnv()
	engine, err := gofresh.New(gofresh.WithDir(moduleDir), gofresh.WithEnv(env...))
	if err != nil {
		return nil, err
	}
	subject := gofresh.Subject{Package: pkg, Symbol: local}
	view, err := engine.NewViewFor([]gofresh.Subject{subject}, moduleDir, gofresh.CodeResult)
	if err != nil {
		return nil, err
	}
	fp, err := view.Capture(subject)
	if err != nil {
		return nil, err
	}
	return &subjectView{symbol: symbol, subject: subject, moduleDir: moduleDir, env: env, view: view, fp: fp}, nil
}

func (s *subjectView) valid(evidence SubjectEvidence) (bool, error) {
	if evidence.Symbol != s.symbol || evidence.RuntimeInputs == "" || evidence.RuntimeDigest == "" {
		return false, nil
	}
	state, err := runtimeinput.CurrentEnv(evidence.RuntimeInputs, s.moduleDir, s.env)
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
	verdict, err := s.view.Check(evidence.fingerprint(), s.subject)
	if err != nil {
		return false, err
	}
	return verdict.Status == gofresh.Valid, nil
}

func (s *subjectView) inspect(evidence SubjectEvidence) (FindingInspection, error) {
	if evidence.Symbol != s.symbol {
		return FindingInspection{State: FindingStale, Reason: "subject identity changed"}, nil
	}
	if evidence.RuntimeUnverifiable {
		return FindingInspection{State: FindingUnverifiable, Reason: evidence.RuntimeReason}, nil
	}
	state, err := runtimeinput.CurrentEnv(evidence.RuntimeInputs, s.moduleDir, s.env)
	if err != nil || !state.OK {
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
	verdict, err := s.view.Check(evidence.fingerprint(), s.subject)
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

// InspectFinding classifies a parsed finding against the current tree without
// running tests (REQ-result-inspection).
func (t *Tree) InspectFinding(f Finding) (FindingInspection, error) {
	declared := t.eng.DeclaredSymbols()
	i := sort.SearchStrings(declared, f.Symbol)
	if i == len(declared) || declared[i] != f.Symbol {
		return FindingInspection{State: FindingDetached, Reason: "mutated symbol no longer resolves"}, nil
	}
	if f.OperatorSet != engine.OperatorSet {
		return FindingInspection{State: FindingStale, Reason: "operator set changed"}, nil
	}
	if _, err := time.ParseDuration(f.Timeout); err != nil {
		return FindingInspection{}, fmt.Errorf("finding %s has invalid timeout: %w", f.Symbol, err)
	}
	if !f.OracleExplicit {
		currentOracle := t.resolveOracle(Target{Symbol: f.Symbol})
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
	target, err := t.newSubjectView(f.Symbol)
	if err != nil {
		return FindingInspection{}, err
	}
	inspection, err := target.inspect(f.TargetEvidence)
	if err != nil || inspection.State != FindingCurrent {
		return inspection, err
	}
	oracle := sortedSubjectEvidence(f.OracleEvidence)
	for _, evidence := range oracle {
		if err := t.eng.ValidateOracle([]string{evidence.Symbol}); err != nil {
			return FindingInspection{State: FindingStale, Reason: "oracle " + evidence.Symbol + " no longer resolves"}, nil
		}
		view, err := t.newSubjectView(evidence.Symbol)
		if err != nil {
			return FindingInspection{}, err
		}
		inspection, err := view.inspect(evidence)
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
		prior.Timeout != current.Timeout || prior.TargetEvidence != current.TargetEvidence ||
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

func evidenceSetMatches(prior Finding, target *subjectView, oracle []*subjectView, oracleExplicit bool, operatorSet, timeout string) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.OracleExplicit != oracleExplicit || prior.Timeout != timeout || len(prior.OracleEvidence) != len(oracle) {
		return false, nil
	}
	ok, err := target.valid(prior.TargetEvidence)
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
		valid, err := subject.valid(evidence)
		if err != nil || !valid {
			return valid, err
		}
	}
	return true, nil
}

func attachEvidence(target *subjectView, oracle []*subjectView, state runtimeinput.State) (SubjectEvidence, []SubjectEvidence, error) {
	attach := func(subject *subjectView) (SubjectEvidence, error) {
		fp := subject.fp
		fp.RuntimeInputs = state.Manifest
		fp.RuntimeDigest = state.Digest
		if err := subject.view.Validate(); err != nil {
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
