package gomutant

import (
	"fmt"
	"sort"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
)

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

func sameAttestationPins(prior, current Finding) bool {
	if prior.OperatorSet != current.OperatorSet || prior.Budget != current.Budget ||
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

func evidenceSetMatches(prior Finding, target *subjectView, oracle []*subjectView, operatorSet, timeout string) (bool, error) {
	if prior.OperatorSet != operatorSet || prior.Timeout != timeout || len(prior.OracleEvidence) != len(oracle) {
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
