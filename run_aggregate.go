package gomutant

import (
	"context"
	"errors"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

func (r *campaignRunner) commitAndAttribute(ctx context.Context, f Finding, w work) error {
	f.Selection = r.selKey
	f = r.annotateCrossSelection(f)
	r.findings[w.target] = f
	if err := commitFinding(ctx, r.opts.Commit, f); err != nil {
		return err
	}
	r.committed[w.target] = true
	r.targetDone(w.target)
	keyFor := baselineKeyFor
	for _, g := range w.groups {
		key := keyFor(g)
		r.groupBudgetMu.Lock()
		pd, pending := r.pendingBankDeposits[key]
		r.groupBudgetMu.Unlock()
		if !pending {
			continue
		}
		rows := bankOracleRowsFor(f, g.pkgs[0])
		if len(rows) == 0 {
			continue
		}
		r.opts.baselineBank.putBaseline(key.persistedKey(r.bankScope), bankedBaseline{
			Evidence: rows, Manifest: pd.manifest, Digest: pd.digest,
			PassedTests:        pd.passed,
			TestDurationMillis: durationsToMillis(pd.testDurations),
			RawMillis:          pd.raw.Milliseconds(), MeasuredAtUnix: time.Now().Unix(),
		})
		r.groupBudgetMu.Lock()
		delete(r.pendingBankDeposits, key)
		r.groupBudgetMu.Unlock()
	}
	return r.t.emitOracleGuidance(ctx, f, w, r.targets[w.target].Symbol, r.opts, r.runEnv, r.guidanceCache)
}

func (r *campaignRunner) aggregateWindow(ctx context.Context, st *executedWindow) error {
	t := r.t
	opts := r.opts
	targets := r.targets
	findings := r.findings
	runEnv := r.runEnv
	oracleMemoryPin := r.oracleMemoryPin
	deriveOracleBudgets := r.deriveOracleBudgets

	window, observations, incompletes, scores := st.window, st.observations, st.incompletes, st.scores
	for wi := range window {
		if st.discard != nil && st.discard[wi] {
			window[wi].candidates = nil
			r.targetDone(window[wi].target)
			continue
		}
		w := window[wi]
		window[wi].candidates = nil
		if err := ctx.Err(); err != nil {
			return err
		}
		if opts.aggregate != nil {
			opts.aggregate()
		}
		f := &findings[w.target]
		if w.serve != nil {
			spliced, err := t.spliceServedFinding(ctx, runEnv, *w.serve, w.candidates, w.flagged, w.baselines, w.targetView, w.oracleViews, w.currentLedger, scores[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Exemptions, oracleMemoryPin, maxGroupBudget(w, opts), deriveOracleBudgets)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !errors.Is(err, errEvidenceFinalization) {
					return err
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			}
			observations[wi] = nil
			if unavailable, err := producerValidation(ctx, w.producer); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			} else if unavailable != "" {
				stampUnverifiable(&spliced, unavailable)
				if unstableForBuckets(&spliced, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, r.coverageCache, 0); err != nil {
						return err
					}
				}
			}
			if stagedDrift, err := t.stampProvenance(ctx, r.repository, w.targetView, w.oracleViews, nil, &spliced); err != nil {
				return err
			} else if stagedDrift != "" {
				r.refuseTarget(w.target, stagedDrift+r.residue())
				continue
			}
			if spliced.EvidenceFault != "" && unstableForBuckets(&spliced, opts.Exemptions) {
				if err := t.bucketSurvivorExecution(ctx, &spliced, w, opts, runEnv, r.coverageCache, 0); err != nil {
					return err
				}
			}
			stampExemptions(&spliced, opts.Exemptions)
			spliced.Run = opts.RunID
			contradictKilledDispositions(spliced.Symbol, w.serve.Attested, spliced.Survivors, spliced.Kills, opts.Contradiction)
			if err := r.commitAndAttribute(ctx, spliced, w); err != nil {
				return err
			}
			continue
		}
		if w.extend != nil {
			extended, err := t.spliceExtendedFinding(ctx, runEnv, *w.extend, w.candidates, w.extendFrom, w.baselines, w.targetView, w.oracleViews, w.currentLedger, scores[wi], observations[wi], incompletes[wi], targets[w.target].Labels, opts.Budget, opts.Exemptions, oracleMemoryPin, maxGroupBudget(w, opts), deriveOracleBudgets)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !errors.Is(err, errEvidenceFinalization) {
					return err
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			}
			observations[wi] = nil
			if unavailable, err := producerValidation(ctx, w.producer); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			} else if unavailable != "" {
				stampUnverifiable(&extended, unavailable)
				if unstableForBuckets(&extended, opts.Exemptions) {
					if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, r.coverageCache, 0); err != nil {
						return err
					}
				}
			}
			if stagedDrift, err := t.stampProvenance(ctx, r.repository, w.targetView, w.oracleViews, nil, &extended); err != nil {
				return err
			} else if stagedDrift != "" {
				r.refuseTarget(w.target, stagedDrift+r.residue())
				continue
			}
			if extended.EvidenceFault != "" && unstableForBuckets(&extended, opts.Exemptions) {
				if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, r.coverageCache, 0); err != nil {
					return err
				}
			} else if !unstableForBuckets(&extended, opts.Exemptions) {
				if err := t.bucketSurvivorExecution(ctx, &extended, w, opts, runEnv, r.coverageCache, len(w.extend.Survivors)); err != nil {
					return err
				}
			}
			stampExemptions(&extended, opts.Exemptions)
			extended.Run = opts.RunID
			if err := r.commitAndAttribute(ctx, extended, w); err != nil {
				return err
			}
			continue
		}
		unionCandidates := w.candidates
		unionOutcomes := scores[wi].outcomes
		unionObservations := observations[wi]
		unionIncompletes := incompletes[wi]
		if w.shaped {
			unionCandidates, unionOutcomes, unionObservations, unionIncompletes = nil, nil, nil, nil
		}
		state, candidateEvidence, err := completedObservationUnion(ctx, t.dir, runEnv, w.baselines, unionCandidates, unionOutcomes, unionObservations, unionIncompletes, nil)
		if err != nil {
			return err
		}
		observations[wi] = nil
		if err := ctx.Err(); err != nil {
			return err
		}
		f.CandidateEvidence = candidateEvidence
		portable := newPortableUnion(state, runEnv)
		if w.shaped {
			oracleEvidence, err := attachOracleEvidence(w.oracleViews, portable)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !errors.Is(err, errEvidenceFinalization) {
					return err
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			}
			f.OracleEvidence = oracleEvidence
		} else {
			targetEvidence, oracleEvidence, err := attachEvidence(w.targetView, w.oracleViews, portable)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !errors.Is(err, errEvidenceFinalization) {
					return err
				}
				r.refuseTarget(w.target, err.Error()+r.residue())
				continue
			}
			f.TargetEvidence = targetEvidence
			f.OracleEvidence = oracleEvidence
			f.CompartmentLedger = compartmentLedgerFromView(w.currentLedger)
		}
		if unavailable, err := producerValidation(ctx, w.producer); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.refuseTarget(w.target, err.Error()+r.residue())
			continue
		} else if unavailable != "" {
			stampUnverifiable(f, unavailable)
		}
		if stagedDrift, err := t.stampProvenance(ctx, r.repository, w.targetView, w.oracleViews, w.shapedFiles, f); err != nil {
			return err
		} else if stagedDrift != "" {
			r.refuseTarget(w.target, stagedDrift+r.residue())
			continue
		}
		f.Run = opts.RunID
		stampExemptions(f, opts.Exemptions)
		*f = mergeScoredFacts(*f, scores[wi], f.OracleMemoryBytes, maxGroupBudget(w, opts), deriveOracleBudgets)
		f.Operators = summarizeOperators(w.candidates, scores[wi].outcomes)
		for _, summary := range f.Operators {
			if err := ctx.Err(); err != nil {
				return err
			}
			f.Discarded += summary.Discarded
			f.Mutants += summary.Killed + summary.Survived
			f.Killed += summary.Killed
		}
		for mi, candidate := range w.candidates {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch scores[wi].outcomes[mi] {
			case engine.MutantSurvived:
				f.Survivors = append(f.Survivors, newSurvivor(candidate, "", scores[wi].flips, mi))
			case engine.MutantKilled:
				f.Kills = append(f.Kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: scores[wi].killers[mi]})
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if w.shaped {
		} else if err := t.bucketSurvivorExecution(ctx, f, w, opts, runEnv, r.coverageCache, 0); err != nil {
			return err
		}
		domainHeld := func(rec Finding) bool {
			if w.shaped {
				return mutationDomainHeld(rec, *f) && shapeEqual(rec.Shape, f.Shape) && sameAttestationPins(rec, *f)
			}
			return mutationDomainHeld(rec, *f)
		}
		if rec, ok := r.prior[targets[w.target].Symbol]; ok && domainHeld(*rec) {
			kept, siteSheds, gone := carryAnchoredAttestations(rec.Attested, f.Survivors)
			f.Attested = append(f.Attested, kept...)
			f.Withdrawn = carryAnchoredWithdrawn(rec.Withdrawn, f.Attested, f.Survivors)
			if !sameAttestationPins(*rec, *f) && opts.AttestationCarried != nil {
				for _, attestation := range kept {
					opts.AttestationCarried(AttestationCarry{
						Symbol: f.Symbol, Position: attestation.Position, Derivation: derivationMove(rec.TargetEvidence, f.TargetEvidence),
						Operator: attestation.Operator, Reason: attestation.Reason,
					})
				}
			}
			for _, d := range siteSheds {
				d.Symbol = f.Symbol
				if opts.AttestationSiteShed != nil {
					opts.AttestationSiteShed(d)
				}
			}
			contradictKilledDispositions(f.Symbol, gone, f.Survivors, f.Kills, opts.Contradiction)
		}
		if err := r.commitAndAttribute(ctx, *f, w); err != nil {
			return err
		}
	}
	return nil
}

func (r *campaignRunner) emitPostures(ctx context.Context, driftedSymbols map[string]bool) {
	t := r.t
	findings := r.findings
	shared := t.newAdmissionShared()
	admissions := make([]judgmentAdmission, len(findings))
	admErrs := make([]error, len(findings))
	cache := newJudgmentCache()
	for i := range findings {
		if ctx.Err() != nil {
			break
		}
		f := findings[i]
		if driftedSymbols[r.targets[i].Symbol] || f.Skipped != "" {
			continue
		}
		adm, err := t.admitFindingContext(ctx, f, shared)
		admissions[i], admErrs[i] = adm, err
		if err != nil {
			continue
		}
		if mv, ok := r.modes[adm.packageProcess]; ok && mv.views != nil {
			cache.queueFindingRows(ctx, f, adm, mv.views.bySymbol)
		}
	}
	if ctx.Err() == nil {
		cache.flush(ctx)
	}
	for i := range findings {
		if ctx.Err() != nil {
			break
		}
		f := findings[i]
		if driftedSymbols[r.targets[i].Symbol] {
			continue
		}
		if f.Skipped != "" {
			r.opts.Posture(RecordPosture{Symbol: f.Symbol, Measurement: "skipped"})
			continue
		}
		var inspection FindingInspection
		adm, err := admissions[i], admErrs[i]
		if err == nil {
			var prebuilt *subjectViewSet
			if mv, ok := r.modes[adm.packageProcess]; ok {
				prebuilt = mv.views
			}
			inspection, err = t.judgeAdmittedContextWithCache(ctx, f, adm, prebuilt, cache)
		}
		if err == nil {
			inspection = withCandidateEvidence(inspection, f)
		}
		if ctx.Err() != nil {
			break
		}
		r.opts.Posture(posture(f, inspection, err))
	}
}
