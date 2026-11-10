package gomutant

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/engine"
)

func (r *campaignRunner) resolveTargetsAndViews(ctx context.Context) error {
	t := r.t
	targets := r.targets
	opts := &r.opts
	preparation := r.preparation
	selKey := r.selKey
	oracleMemoryPin := r.oracleMemoryPin
	deriveOracleBudgets := r.deriveOracleBudgets
	findings := r.findings

	for i, tg := range targets {
		if err := ctx.Err(); err != nil {
			return err
		}
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationResolving, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return err
		}
		f := &findings[i]
		*f = Finding{Symbol: tg.Symbol, Selection: selKey, Labels: tg.Labels, OperatorSet: engine.OperatorSet, OracleExplicit: tg.explicitOracle(), OracleTimeout: opts.OracleTimeout.String(), OracleMemoryBytes: oracleMemoryPin}
		if deriveOracleBudgets {
			// The derived record's bound starts at the floor and raises
			// to the loosest budget any verdict runs under
			// (mergeScoredFacts); the leash is a measurement ceiling,
			// never a recorded bound.
			f.OracleTimeout = seams.ephemeralBudgetFloor.String()
			f.OracleTimeoutDerived = true
		}
		if tg.Shaped() {
			// Validation precedes oracle derivation: a shaped identity
			// resolves to no package, so deriving an oracle from it
			// would be a run fault where the contract wants a
			// target-local refusal (an explicit oracle is required).
			if err := validateShapedTarget(tg); err != nil {
				r.skipTarget(ctx, i, "shaped target refused: "+err.Error(), false)
				continue
			}
		}
		oracle, oracleStoodDown, oracleDerived, err := preparation.oracle(ctx, tg)
		if err != nil {
			return err
		}
		// The derivation names its own caps and breadth: the measure
		// decision carries how wide the derived oracle is and which
		// test-bearing packages the derivation could not consult — a
		// silently narrowed oracle is the defect class this derivation
		// exists to close (REQ-target-default's stand-down naming).
		oracleNote := ""
		if len(tg.Oracle) == 0 && !tg.OracleExplicit {
			if len(oracle) > 0 {
				oracleNote = fmt.Sprintf("; oracle: %s across %s", testNoun(len(oracle)), packageNoun(len(pkgRuns(oracle))))
			}
			if len(oracleStoodDown) > 0 {
				oracleNote += "; derivation stood down on: " + strings.Join(oracleStoodDown, ", ")
			}
		}
		if len(oracle) == 0 {
			// Nothing can kill: the caller sees it and decides
			// (REQ-target-default). An oracle that exists but could not
			// be consulted is NOT "no oracle" — the stood-down packages
			// and the escape ride the skip, the naming duty's worst
			// case (a dark symbol filed as unwitnessed when a consumer
			// suite merely failed to resolve).
			reason := "no oracle"
			if len(oracleStoodDown) > 0 {
				reason += " — derivation stood down on: " + strings.Join(oracleStoodDown, ", ") + "; resolve the closure, or pass an explicit oracle"
			}
			r.skipTarget(ctx, i, reason, false)
			// A derivation that ran over the target's resolved package
			// and found no test with nothing stood down is the
			// selection's coverage bound: no test of the declared leg
			// reaches the target. An explicit empty statement and an
			// unresolved symbol never are (REQ-result-unreached-bound).
			findings[i].Unreached = oracleDerived && len(oracleStoodDown) == 0
			continue
		}
		if err := preparation.validateOracle(ctx, oracle); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Target-local: one target's broken oracle set never takes
			// sibling targets down (REQ-exec-quiescence's locality,
			// which governs resolution and decision evidence exactly as
			// it governs freshness-proof construction).
			r.skipTarget(ctx, i, "oracle validation failed: "+err.Error(), false)
			continue
		}
		if tg.Shaped() {
			// A shaped target resolves from its declared parameters:
			// no body, no catalog, its own pin (REQ-target-structural,
			// REQ-target-manual-recipes). Refusals are target-local
			// skips exactly as resolution failures are.
			candidates, digest, shapedFiles, err := t.shapedCandidates(ctx, tg)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// A drift-shaped cause (a recipe or probe file moved or
				// vanished since load) refuses into the operational-
				// failure set exactly as the phase-one paths do — the
				// amended contract holds universally, shaped targets
				// included (REQ-exec-quiescence).
				var sourceDrift *engine.SourceDriftError
				if errors.As(err, &sourceDrift) || errors.Is(err, iofs.ErrNotExist) {
					r.refuseSkipped(ctx, i, "shaped source drifted since load: "+err.Error()+" - re-run when the tree settles", false)
					continue
				}
				r.skipTarget(ctx, i, "shaped resolution failed: "+err.Error(), false)
				continue
			}
			f.BodyHash = digest
			f.OperatorSet = shapedOperatorSet
			f.Shape = &TargetShape{Structural: tg.Structural, Manual: tg.Manual}
			r.resolvedTargets = append(r.resolvedTargets, resolvedTarget{index: i, oracle: oracle, oracleNote: oracleNote, shaped: candidates, shapedDigest: digest, shapedFiles: shapedFiles, attested: packageProcessAttestable(t.PackageOf, tg.Symbol, oracle)})
			continue
		}
		bodyHash, err := t.eng.BodyHashContext(ctx, tg.Symbol)
		if errors.Is(err, engine.ErrNotFunction) {
			// A type or variable target is a legitimate reference with no
			// body to mutate: reported, never fatal, never silently dropped.
			r.skipTarget(ctx, i, "not a function - for mutation adequacy, target its methods or the bound function-level subjects", false)
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// A resolution failure caused by a loaded file VANISHING is
			// drift exactly like a modified one (a checkout mid-run) —
			// refused into the operational-failure set, never a quiet
			// skip a pipeline reads as success. Every other typed-load
			// breakage stays this target's own skip with the cause
			// (REQ-exec-quiescence).
			if errors.Is(err, iofs.ErrNotExist) {
				r.refuseSkipped(ctx, i, "source vanished since load: "+err.Error()+" - re-run when the tree settles", false)
				continue
			}
			r.skipTarget(ctx, i, "target resolution failed: "+err.Error(), false)
			continue
		}
		f.BodyHash = bodyHash
		reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationFreshness, Symbol: tg.Symbol})
		if err := ctx.Err(); err != nil {
			return err
		}
		r.resolvedTargets = append(r.resolvedTargets, resolvedTarget{index: i, oracle: oracle, oracleNote: oracleNote, attested: packageProcessAttestable(t.PackageOf, tg.Symbol, oracle)})
	}
	for _, resolved := range r.resolvedTargets {
		mv := r.modeFor(resolved.attested)
		if resolved.shaped == nil {
			mv.symbols = append(mv.symbols, targets[resolved.index].Symbol)
		}
		mv.symbols = append(mv.symbols, resolved.oracle...)
	}
	if err := r.eachMode(func(mv *modeViews) error {
		if len(mv.symbols) == 0 {
			return nil
		}
		reportPreparation(opts.Progress, pricedPass(PreparationViews, mv.symbols, t.PackageOf))
		var err error
		mv.views, mv.viewFaults, err = t.buildSubjectViews(ctx, mv.symbols, preparation.packageContext, mv.engines)
		if err != nil {
			return fmt.Errorf("freshness: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	// Decision-evidence faults route target-locally exactly as the
	// observed union's do (REQ-exec-quiescence): a target whose own
	// symbol or any of whose oracle symbols has no decision view skips
	// with the cause, and its siblings proceed.
	{
		kept := r.resolvedTargets[:0]
		for _, rt := range r.resolvedTargets {
			mv := r.modeFor(rt.attested)
			var cause error
			needed := rt.oracle
			if rt.shaped == nil {
				needed = append([]string{targets[rt.index].Symbol}, rt.oracle...)
			}
			for _, symbol := range needed {
				if fault, ok := mv.viewFaults[symbol]; ok {
					cause = fault
					break
				}
				if mv.views.bySymbol[symbol] == nil {
					cause = fmt.Errorf("no decision view for %s", symbol)
					break
				}
			}
			if cause == nil {
				kept = append(kept, rt)
				continue
			}
			r.skipTarget(ctx, rt.index, "decision evidence unavailable: "+cause.Error(), false)
		}
		r.resolvedTargets = kept
	}
	opts.probeGate = &r.probeGate
	if seams.probeGateInstalled != nil {
		seams.probeGateInstalled(&r.probeGate)
	}
	unitOf := make([]*proofUnit, len(targets))
	for _, resolved := range r.resolvedTargets {
		var pkgs []string
		for _, run := range pkgRuns(resolved.oracle) {
			pkgs = append(pkgs, run.pkg)
		}
		key := fmt.Sprintf("%t\x00%s", resolved.attested, strings.Join(pkgs, "\x00"))
		u, ok := r.proofUnits[key]
		if !ok {
			mv := r.modes[resolved.attested]
			u = &proofUnit{key: key, packages: pkgs, decision: mv.views, engines: mv.engines}
			r.proofUnits[key] = u
		}
		if resolved.shaped == nil {
			u.symbols = append(u.symbols, targets[resolved.index].Symbol)
		}
		u.symbols = append(u.symbols, resolved.oracle...)
		u.remaining++
		unitOf[resolved.index] = u
	}
	for _, u := range r.proofUnits {
		sort.Strings(u.symbols)
		u.symbols = slices.Compact(u.symbols)
	}
	r.unitDone = make([]bool, len(targets))
	r.unitOf = unitOf

	seenOraclePackage := map[string]bool{}
	for _, resolved := range r.resolvedTargets {
		if err := ctx.Err(); err != nil {
			return err
		}
		var targetPkg string
		if resolved.shaped == nil {
			mv := r.modeFor(resolved.attested)
			if tv := mv.views.bySymbol[targets[resolved.index].Symbol]; tv != nil {
				targetPkg = tv.subject.Package
			}
		}
		for _, run := range pkgRuns(resolved.oracle) {
			if !seenOraclePackage[run.pkg] {
				seenOraclePackage[run.pkg] = true
				r.oraclePackages = append(r.oraclePackages, run.pkg)
			}
			if targetPkg != "" && !slices.Contains(r.coverPkgsByOraclePkg[run.pkg], targetPkg) {
				r.coverPkgsByOraclePkg[run.pkg] = append(r.coverPkgsByOraclePkg[run.pkg], targetPkg)
			}
		}
	}
	for pkg := range r.coverPkgsByOraclePkg {
		slices.Sort(r.coverPkgsByOraclePkg[pkg])
	}
	r.preflightBracket = sync.OnceValue(func() error {
		return preflightBracketPaths(ctx, t.dir, opts.BracketPaths)
	})
	return nil
}

func (r *campaignRunner) preflightOracleModules(_ []string) error {
	if len(r.opts.BracketPaths) == 0 {
		return nil
	}
	return r.preflightBracket()
}

func (r *campaignRunner) buildProofUnit(ctx context.Context, u *proofUnit) error {
	if u.built {
		return nil
	}
	u.built = true
	if r.opts.proofAttempt != nil {
		r.opts.proofAttempt(strings.Join(u.packages, ","), 1)
	}
	reportPreparation(r.opts.Progress, pricedPass(PreparationProofs, u.symbols, r.t.PackageOf))
	r.probeGate.RLock()
	defer r.probeGate.RUnlock()
	union, faults, err := u.decision.observedFor(ctx, u.symbols)
	if err != nil {
		return fmt.Errorf("freshness proofs (unit over %d subjects): %w", len(u.symbols), err)
	}
	u.union, u.faults = union, faults
	return nil
}

func (r *campaignRunner) producerViewsFor(ctx context.Context, u *proofUnit, target string, oracle []string, rebuild []string) (views *subjectViewSet, fault, err error) {
	if err := r.buildProofUnit(ctx, u); err != nil {
		return nil, nil, err
	}
	views, fault = u.union.forTarget(target, oracle, u.faults)
	if fault != nil && ctx.Err() == nil {
		if r.opts.proofAttempt != nil {
			r.opts.proofAttempt(target, 2)
		}
		r.probeGate.RLock()
		var rebuilt *observedViewSet
		rebuilt, fault = r.t.newStrictObservedViews(ctx, rebuild, r.preparation.packageContext, u.engines)
		if fault == nil {
			views = rebuilt.subjectViewSet
		}
		r.probeGate.RUnlock()
	}
	return views, fault, nil
}

func (r *campaignRunner) setupGroupBudgetServices() {
	keyFor := baselineKeyFor
	r.opts.groupBudget = func(g group) time.Duration {
		if !r.deriveOracleBudgets {
			return r.opts.OracleTimeout
		}
		r.groupBudgetMu.Lock()
		defer r.groupBudgetMu.Unlock()
		if b, ok := r.groupBaselines[keyFor(g)]; ok && b.budget > 0 {
			return b.budget
		}
		return r.opts.OracleTimeout
	}
	r.opts.groupLeash = func(g group) time.Duration {
		if !r.deriveOracleBudgets {
			return r.opts.OracleTimeout
		}
		r.groupBudgetMu.Lock()
		defer r.groupBudgetMu.Unlock()
		b, ok := r.groupBaselines[keyFor(g)]
		if !ok {
			return r.opts.OracleTimeout
		}
		return leashFor(r.opts.OracleTimeout, b.raw)
	}
	r.opts.baselineDur = func(g group) (time.Duration, bool) {
		if seams.baselinePrice != nil {
			if d, ok := seams.baselinePrice(g.pkgs); ok {
				return d, true
			}
		}
		r.groupBudgetMu.Lock()
		defer r.groupBudgetMu.Unlock()
		b, ok := r.groupBaselines[keyFor(g)]
		return b.raw, ok
	}
	r.opts.foldedCoverage = func(g group, coverPkg string) (engine.Coverage, bool) {
		r.groupBudgetMu.Lock()
		defer r.groupBudgetMu.Unlock()
		b, ok := r.groupBaselines[keyFor(g)]
		if !ok || b.coverage == nil || !slices.Contains(b.coverPkgs, coverPkg) {
			return engine.Coverage{}, false
		}
		return *b.coverage, true
	}
}

func (r *campaignRunner) probeGroupBaselines(ctx context.Context, tg Target, w *work) (string, error) {
	t := r.t
	opts := &r.opts
	keyFor := baselineKeyFor
	deriveOracleBudgets := r.deriveOracleBudgets
	bankScope := r.bankScope
	runEnv := r.runEnv

	w.baselines = make([]gofresh.Observation, 0, len(w.groups))
	for gi := range w.groups {
		group := w.groups[gi]
		key := keyFor(group)
		if reason, failed := r.baselineFailures[key]; failed {
			return reason, nil
		}
		state, ok := r.baselineCache[key]
		var banked bankedBaseline
		var hit bool
		if !ok {
			banked, hit = opts.baselineBank.baseline(key.persistedKey(bankScope))
			if !opts.Force && hit {
				views := groupOracleViews(*w, group)
				held, herr := bankPinsHold(ctx, banked.Evidence, views)
				if herr != nil && ctx.Err() != nil {
					return "", ctx.Err()
				}
				if herr == nil && held && len(banked.PassedTests) > 0 {
					adopted, aerr := gofresh.AdoptObservation(banked.Manifest, t.dir, fmt.Sprintf("gomutant-banked-baseline-%d", findingObservationSequence.Add(1)), runEnv)
					if aerr == nil && adopted.Digest == banked.Digest {
						reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0], Banked: true})
						raw := time.Duration(banked.RawMillis) * time.Millisecond
						testDurs := millisToDurations(banked.TestDurationMillis)
						if deriveOracleBudgets {
							budget := derivedOracleBudget(raw)
							r.groupBudgetMu.Lock()
							r.groupBaselines[key] = groupBaseline{raw: raw, budget: budget, passed: banked.PassedTests, testDurations: testDurs}
							r.groupBudgetMu.Unlock()
							reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationOracleBudget, Symbol: tg.Symbol, Package: group.pkgs[0], OracleBudget: budget.String()})
						} else {
							r.groupBudgetMu.Lock()
							r.groupBaselines[key] = groupBaseline{raw: raw, passed: banked.PassedTests, testDurations: testDurs}
							r.groupBudgetMu.Unlock()
						}
						state, ok = adopted, true
						r.baselineCache[key] = state
					}
				}
			}
		}
		if !ok {
			reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationBaseline, Symbol: tg.Symbol, Package: group.pkgs[0]})
			if err := ctx.Err(); err != nil {
				return "", err
			}
			baselineBound := opts.OracleTimeout
			if deriveOracleBudgets {
				baselineBound = leashFor(opts.OracleTimeout, time.Duration(banked.RawMillis)*time.Millisecond)
			}
			coverPkgs := r.coverPkgsByOraclePkg[group.pkgs[0]]
			if !w.shaped && w.targetView != nil && w.targetView.subject.Package != "" && !slices.Contains(coverPkgs, w.targetView.subject.Package) {
				coverPkgs = append(slices.Clone(coverPkgs), w.targetView.subject.Package)
				slices.Sort(coverPkgs)
			}
			probeCtx := ctx
			if !w.shaped && seams.hasDefaultCoveredPositions() && len(coverPkgs) > 0 {
				probeCtx = engine.WithBaselineCoverage(ctx, coverPkgs, t.eng.DirectiveCoverage())
			}
			baselineStart := time.Now()
			r.probeGate.RLock()
			baseline, err := seams.baselineProbe(probeCtx, t.dir, group.pkgs[0], group.runRegex, baselineBound, group.flags, group.moduleDir, group.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
			r.probeGate.RUnlock()
			ran, passed, failedTests, diagnostic, observed := baseline.Ran, baseline.Passed, baseline.Failed, baseline.Diagnostic, baseline.Observation
			baselineElapsed := time.Since(baselineStart)
			var reason string
			switch {
			case err != nil && ctx.Err() != nil:
				return "", ctx.Err()
			case err != nil:
				reason = fmt.Sprintf("oracle baseline probe failed in %s: %v", group.pkgs[0], err)
			case ran == 0:
				reason = fmt.Sprintf("oracle baseline matched no tests in %s", group.pkgs[0])
			case !passed:
				reason = fmt.Sprintf("oracle baseline does not pass in %s (failed: %s)", group.pkgs[0], strings.Join(failedTests, ", "))
			}
			if reason != "" && diagnostic != "" && opts.AnalysisEvent != nil {
				opts.AnalysisEvent(AnalysisEvent{Phase: "baseline-output", Package: group.pkgs[0], Detail: diagnostic})
			}
			if reason != "" && len(tg.Oracle) == 0 && !tg.OracleExplicit && w.targetView != nil && group.pkgs[0] != w.targetView.subject.Package {
				reason += fmt.Sprintf(" — a consumer package of the derived oracle; fix its suite, or pass an explicit oracle to measure %s without it", tg.Symbol)
			}
			if reason != "" {
				r.baselineFailures[key] = reason
				return reason, nil
			}
			entry := groupBaseline{
				raw:           baselineElapsed,
				passed:        baseline.Tests,
				testDurations: baseline.TestDurations,
				coverage:      baseline.Coverage,
				coverPkgs:     coverPkgs,
			}
			if deriveOracleBudgets {
				entry.budget = derivedOracleBudget(baselineElapsed)
			}
			r.groupBudgetMu.Lock()
			r.groupBaselines[key] = entry
			r.groupBudgetMu.Unlock()
			if !observed.Unverifiable && observed.Manifest != "" {
				deposited, derr := depositGroupBaseline(ctx, opts.baselineBank, key.persistedKey(bankScope), *w, group, observed, runEnv, baselineElapsed, baseline.Tests, baseline.TestDurations)
				if derr != nil {
					return "", derr
				}
				if !deposited {
					r.groupBudgetMu.Lock()
					r.pendingBankDeposits[key] = pendingBankDeposit{
						manifest:      observed.Manifest,
						digest:        observed.Digest,
						raw:           baselineElapsed,
						passed:        baseline.Tests,
						testDurations: baseline.TestDurations,
					}
					r.groupBudgetMu.Unlock()
				}
			}
			if deriveOracleBudgets {
				reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationOracleBudget, Symbol: tg.Symbol, Package: group.pkgs[0], OracleBudget: entry.budget.String()})
			}
			state = observed
			if err := ctx.Err(); err != nil {
				return "", err
			}
			r.baselineCache[key] = state
		}
		w.baselines = append(w.baselines, state)
		r.groupBudgetMu.Lock()
		w.groups[gi].passed = r.groupBaselines[key].passed
		r.groupBudgetMu.Unlock()
	}
	if !w.shaped && len(w.groups) > 1 && w.targetView != nil {
		orderOracleGroups(
			w,
			tg.Symbol,
			w.targetView.subject.Package,
			t.eng.ReachesSymbol,
			func(g group) (time.Duration, map[string]time.Duration) {
				if seams.baselinePrice != nil {
					if d, ok := seams.baselinePrice(g.pkgs); ok {
						return d, nil
					}
				}
				r.groupBudgetMu.Lock()
				defer r.groupBudgetMu.Unlock()
				b := r.groupBaselines[keyFor(g)]
				return b.raw, b.testDurations
			},
		)
	}
	return "", nil
}

func (r *campaignRunner) prepareShaped(ctx context.Context, resolved resolvedTarget) (*work, error) {
	t := r.t
	opts := &r.opts
	i := resolved.index
	tg := r.targets[i]
	f := &r.findings[i]
	mv := r.modeFor(resolved.attested)
	oracle := resolved.oracle
	oracleViews := make([]*subjectView, 0, len(oracle))
	for _, symbol := range oracle {
		oracleViews = append(oracleViews, mv.views.bySymbol[symbol])
	}
	rec, hasPrior := r.prior[tg.Symbol]
	targetRapid, err := r.preparation.rapidPackages(ctx, r.oraclePackages)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.skipTarget(ctx, i, "property-runtime detection unavailable: "+err.Error(), true)
		return nil, nil
	}
	regime := ""
	for _, run := range pkgRuns(oracle) {
		if targetRapid[run.pkg] {
			regime = engine.PropertyRegimeRapid
			break
		}
	}
	f.PropertyRegime = regime
	f.OracleExecutionPolicy = FullOracleExecutionPolicy
	reason := "no-prior"
	var decision RunDecision
	if hasPrior && opts.Force {
		reason = "forced"
	}
	if hasPrior && !opts.Force {
		matches, err := shapedEvidenceMatchesContext(ctx, *rec, oracleViews, shapedOperatorSet, opts.OracleTimeout.String(), r.deriveOracleBudgets, r.oracleMemoryPin, regime)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			r.refuseSkipped(ctx, i, serveCheckRefusal(err), true)
			return nil, nil
		}
		if matches && rec.BodyHash == resolved.shapedDigest && shapeEqual(rec.Shape, &TargetShape{Structural: tg.Structural, Manual: tg.Manual}) {
			served := snapshotFindings([]Finding{*rec})[0]
			served.Selection = r.selKey
			served.Labels = append([]string(nil), tg.Labels...)
			served.Cached = true
			if stagedDrift, err := t.stampProvenance(ctx, r.repository, nil, oracleViews, resolved.shapedFiles, &served); err != nil {
				return nil, err
			} else if stagedDrift != "" {
				r.refuseSkipped(ctx, i, "staged drift: "+stagedDrift, true)
				return nil, nil
			}
			served = r.annotateCrossSelection(served)
			*f = served
			r.cacheTarget(ctx, i, "served: shape and oracle pins unchanged"+resolved.oracleNote)
			return nil, nil
		}
		reason = "stale"
		if issue := OracleExecutionPolicyIssue(*rec); issue != "" {
			reason = "stale: " + issue
		} else if !matches {
			reason = "stale: an oracle or measurement pin moved"
		} else if rec.BodyHash != resolved.shapedDigest {
			reason = "stale: the declared shape or a probed file moved"
		}
	}
	if err := r.preflightOracleModules(oracle); err != nil {
		return nil, err
	}
	var producerViews *subjectViewSet
	if !opts.PlanOnly {
		views, fault, err := r.producerViewsFor(ctx, r.unitOf[i], oracle[0], oracle[1:], oracle)
		if err != nil {
			return nil, err
		}
		if fault != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			r.skipTarget(ctx, i, "producer evidence unavailable: "+fault.Error(), true)
			return nil, nil
		}
		producerViews = views
	}
	for _, oracleView := range oracleViews {
		oracleView.module.producer = true
	}
	if producerViews != nil {
		oracleViews = oracleViews[:0]
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
		}
		for _, module := range producerViews.modules {
			module.producer = true
		}
	}
	item := work{target: i, oracle: oracle, reason: reason, shaped: true, candidates: resolved.shaped, shapedFiles: resolved.shapedFiles, oracleViews: oracleViews, producer: producerViews}
	w := &item
	oracleSet := make(map[string]bool, len(oracle))
	for _, o := range oracle {
		oracleSet[o] = true
	}
	w.oracleSet = oracleSet
	decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: reason + resolved.oracleNote, Candidates: len(w.candidates)}
	f.Budget = 0
	f.CandidateCount = len(w.candidates)
	f.Generated = len(w.candidates)
	reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
	if opts.PlanOnly {
		if opts.Decision != nil {
			opts.Decision(decision)
		}
		return nil, nil
	}
	runs := pkgRuns(w.oracle)
	runtimes, err := r.preparation.propertyRuntimes(ctx, r.oraclePackages)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.skipTarget(ctx, i, "property-runtime detection unavailable: "+err.Error(), true)
		return nil, nil
	}
	for _, pr := range runs {
		flags := propertyOracleFlags(slices.Contains(runtimes[pr.pkg], "rapid"))
		for _, runtime := range runtimes[pr.pkg] {
			note, ok := propertyOracleNote(pr.pkg, runtime)
			if ok && r.preparation.noteProperty(pr.pkg+"/"+runtime) && opts.PropertyOracle != nil {
				opts.PropertyOracle(note)
			}
		}
		moduleDir, packageDir, err := r.preparation.packageContext(ctx, pr.pkg)
		if err != nil {
			return nil, err
		}
		w.groups = append(w.groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
	}
	if reason, err := r.probeGroupBaselines(ctx, tg, w); err != nil {
		return nil, err
	} else if reason != "" {
		r.skipTarget(ctx, i, reason, true)
		return nil, nil
	}
	if opts.Decision != nil {
		opts.Decision(decision)
	}
	r.preparedTargets.Add(1)
	r.preparedCandidates.Add(int64(len(w.candidates)))
	return w, nil
}

func (r *campaignRunner) prepareTarget(ctx context.Context, resolved resolvedTarget) (*work, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t := r.t
	opts := &r.opts
	i := resolved.index
	tg := r.targets[i]
	f := &r.findings[i]
	mv := r.modeFor(resolved.attested)
	oracle := resolved.oracle
	targetView := mv.views.bySymbol[tg.Symbol]
	oracleViews := make([]*subjectView, 0, len(oracle))
	for _, symbol := range oracle {
		oracleViews = append(oracleViews, mv.views.bySymbol[symbol])
	}
	if r.repository.staged {
		external, unlisted, err := t.externalInputs(ctx, r.repository, targetView, oracleViews, resolved.shapedFiles)
		if err != nil {
			return nil, err
		}
		if len(unlisted) != 0 {
			r.refuseSkipped(ctx, i, unlistedRefusal(unlisted, "; measure unstaged"), true)
			return nil, nil
		}
		if len(external) != 0 {
			r.refuseSkipped(ctx, i, "measured input outside the repository: "+cappedJoin(external, 8)+" - the index snapshot cannot vouch for it; measure unstaged", true)
			return nil, nil
		}
	}
	if resolved.shaped != nil {
		return r.prepareShaped(ctx, resolved)
	}
	driftSkip := func(err error) bool {
		var sourceDrift *engine.SourceDriftError
		if !errors.As(err, &sourceDrift) {
			return false
		}
		r.refuseSkipped(ctx, i, "source changed since load: "+sourceDrift.Path+" - re-run when the tree settles", true)
		return true
	}
	rec, hasPrior := r.prior[tg.Symbol]
	targetRapid, err := r.preparation.rapidPackages(ctx, r.oraclePackages)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.skipTarget(ctx, i, "property-runtime detection unavailable: "+err.Error(), true)
		return nil, nil
	}
	regime := ""
	for _, run := range pkgRuns(oracle) {
		if targetRapid[run.pkg] {
			regime = engine.PropertyRegimeRapid
			break
		}
	}
	f.PropertyRegime = regime
	f.OracleExecutionPolicy = FullOracleExecutionPolicy
	reason := "no-prior"
	if hasPrior {
		switch {
		case opts.Force:
			reason = "forced"
		case !budgetCovers(*rec, opts.Budget):
			reason = fmt.Sprintf("budget: prior generated %d of %d candidates, request wants budget %d", rec.Generated, rec.CandidateCount, opts.Budget)
		default:
			reason = "stale"
		}
	}
	var serve *Finding
	refuseServeCheck := func(reason string) {
		r.refuseSkipped(ctx, i, reason, true)
	}
	if hasPrior && !opts.Force && budgetCovers(*rec, opts.Budget) {
		matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), r.deriveOracleBudgets, r.oracleMemoryPin, regime)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			refuseServeCheck(serveCheckRefusal(err))
			return nil, nil
		}
		if !matches {
			r.probeGate.RLock()
			reason = t.movedPinAttribution(ctx, *rec, mv.views, "stale: a measurement pin moved (oracle timeout, oracle selection, operator set, or runtime inputs moved during evaluation)")
			r.probeGate.RUnlock()
		}
		if matches && len(rec.CandidateEvidence) == 0 {
			cached := *rec
			cached.Selection = r.selKey
			cached.Labels = append([]string(nil), tg.Labels...)
			cached.Cached = true
			if stagedDrift, err := t.stampProvenance(ctx, r.repository, targetView, oracleViews, nil, &cached); err != nil {
				return nil, err
			} else if stagedDrift != "" {
				r.refuseSkipped(ctx, i, "staged drift: "+stagedDrift, true)
				return nil, nil
			}
			cached = r.annotateCrossSelection(cached)
			r.findings[i] = cached
			if err := commitFinding(ctx, opts.Commit, cached); err != nil {
				return nil, err
			}
			r.cacheTarget(ctx, i, "served: body, oracle closure, and runtime inputs unchanged"+resolved.oracleNote)
			return nil, nil
		}
		if matches {
			snapshot := snapshotFindings([]Finding{*rec})[0]
			serve = &snapshot
		}
	}
	var extend *Finding
	if hasPrior && !opts.Force && !budgetCovers(*rec, opts.Budget) {
		if len(rec.CandidateEvidence) != 0 {
			reason += "; prior candidate evidence re-executes only under its recorded budget, so the whole target re-measures"
			if issue := OracleExecutionPolicyIssue(*rec); issue != "" {
				reason += "; " + issue
			}
		} else {
			matches, err := evidenceSetMatchesContext(ctx, *rec, targetView, oracleViews, f.OracleExplicit, engine.OperatorSet, opts.OracleTimeout.String(), r.deriveOracleBudgets, r.oracleMemoryPin, regime)
			if err != nil {
				return nil, err
			}
			if matches {
				snapshot := snapshotFindings([]Finding{*rec})[0]
				extend = &snapshot
			} else {
				r.probeGate.RLock()
				attribution := t.movedPinAttribution(ctx, *rec, mv.views, "a measurement pin also moved, so the measured prefix cannot stand")
				r.probeGate.RUnlock()
				reason += "; " + attribution
			}
		}
	}

	if err := r.preflightOracleModules(oracle); err != nil {
		return nil, err
	}
	var producerViews *subjectViewSet
	if !opts.PlanOnly {
		views, fault, err := r.producerViewsFor(ctx, r.unitOf[i], tg.Symbol, oracle, append([]string{tg.Symbol}, oracle...))
		if err != nil {
			return nil, err
		}
		if fault != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, proofAbortError(tg.Symbol, oracle, fault, ctxErr)
			}
			r.skipTarget(ctx, i, fmt.Sprintf("freshness proof unavailable (oracle %s): %v", strings.Join(oracle, ", "), fault), true)
			return nil, nil
		}
		producerViews = views
	}
	targetView.module.producer = true
	for _, oracleView := range oracleViews {
		oracleView.module.producer = true
	}
	if opts.producer != nil {
		opts.producer(tg.Symbol)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if producerViews != nil {
		targetView = producerViews.bySymbol[tg.Symbol]
		oracleViews = oracleViews[:0]
		for _, symbol := range oracle {
			oracleViews = append(oracleViews, producerViews.bySymbol[symbol])
		}
		for _, module := range producerViews.modules {
			module.producer = true
		}
	}
	oracleSet := make(map[string]bool, len(oracle))
	for _, o := range oracle {
		oracleSet[o] = true
	}
	currentLedger, err := targetView.view.TestVariantLedger(targetView.subject)
	if err != nil {
		return nil, err
	}
	item := work{target: i, oracle: oracle, reason: reason, oracleSet: oracleSet, targetView: targetView, oracleViews: oracleViews, producer: producerViews, currentLedger: currentLedger, serve: serve, extend: extend}
	w := &item
	var decision RunDecision
	reportPreparation(opts.Progress, PreparationEvent{Stage: PreparationMutants, Symbol: tg.Symbol})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	budget := opts.Budget
	if w.serve != nil {
		budget = w.serve.Budget
	}
	regenerateAtBudget := func() (engine.Generation, bool, error) {
		g, gerr := t.eng.CandidatesContext(ctx, tg.Symbol, opts.Budget)
		if gerr != nil {
			if driftSkip(gerr) {
				return engine.Generation{}, true, nil
			}
			return engine.Generation{}, false, fmt.Errorf("target %s: %w", tg.Symbol, gerr)
		}
		return g, false, nil
	}
	generation, err := t.eng.CandidatesContext(ctx, tg.Symbol, budget)
	if err != nil {
		if driftSkip(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("target %s: %w", tg.Symbol, err)
	}
	if w.serve != nil {
		if flagged, ok := flaggedCandidateIndexes(generation, *w.serve); ok {
			w.candidates = generation.Candidates
			w.flagged = flagged
			decision = RunDecision{Symbol: tg.Symbol, Action: "cached", Reason: fmt.Sprintf("served: pins unchanged; re-executing %s", candidateNoun(len(flagged))) + resolved.oracleNote, Candidates: len(flagged)}
		} else {
			w.serve, w.flagged = nil, nil
			if budget != opts.Budget {
				var regenerated bool
				if generation, regenerated, err = regenerateAtBudget(); regenerated {
					return nil, nil
				} else if err != nil {
					return nil, err
				}
			}
		}
	}
	if w.extend != nil {
		if extendedPrefixStands(generation, *w.extend) {
			w.candidates = generation.Candidates
			w.extendFrom = w.extend.Generated
			suffix := len(generation.Candidates) - w.extendFrom
			decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: fmt.Sprintf("served: prefix of %s stands; measuring %d more", candidateNoun(w.extendFrom), suffix) + resolved.oracleNote, Candidates: suffix}
		} else {
			w.extend = nil
			w.reason += "; deterministic regeneration cannot re-identify the measured prefix"
		}
	}
	if w.serve == nil && w.extend == nil {
		w.candidates = generation.Candidates
		decision = RunDecision{Symbol: tg.Symbol, Action: "measure", Reason: w.reason + resolved.oracleNote, Candidates: len(generation.Candidates)}
		f.Budget = opts.Budget
		f.CandidateCount = generation.CandidateCount
		f.Generated = len(generation.Candidates)
	}
	if opts.PlanOnly {
		if opts.Decision != nil {
			opts.Decision(decision)
		}
		return nil, nil
	}
	runtimes, err := r.preparation.propertyRuntimes(ctx, r.oraclePackages)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.skipTarget(ctx, i, "property-runtime detection unavailable: "+err.Error(), true)
		return nil, nil
	}
	buildGroups := func(oracle []string) ([]group, error) {
		var groups []group
		for _, pr := range pkgRuns(oracle) {
			flags := propertyOracleFlags(slices.Contains(runtimes[pr.pkg], "rapid"))
			for _, runtime := range runtimes[pr.pkg] {
				note, ok := propertyOracleNote(pr.pkg, runtime)
				if ok && r.preparation.noteProperty(pr.pkg+"/"+runtime) && opts.PropertyOracle != nil {
					opts.PropertyOracle(note)
				}
			}
			moduleDir, packageDir, err := r.preparation.packageContext(ctx, pr.pkg)
			if err != nil {
				return nil, err
			}
			groups = append(groups, group{pkgs: []string{pr.pkg}, runRegex: pr.runRegex, flags: flags, moduleDir: moduleDir, packageDir: packageDir})
		}
		return groups, nil
	}
	if w.groups, err = buildGroups(w.oracle); err != nil {
		return nil, err
	}
	if reason, err := r.probeGroupBaselines(ctx, tg, w); err != nil {
		return nil, err
	} else if reason != "" {
		r.skipTarget(ctx, i, reason, true)
		return nil, nil
	}
	if opts.Decision != nil {
		opts.Decision(decision)
	}
	r.preparedTargets.Add(1)
	r.preparedCandidates.Add(int64(len(w.candidates)))
	return w, nil
}

func (r *campaignRunner) deliverPrepared(ctx context.Context, emit func(work) error) error {
	next := 0
	for i := range r.targets {
		if next < len(r.resolvedTargets) && r.resolvedTargets[next].index == i {
			w, err := r.prepareTarget(ctx, r.resolvedTargets[next])
			next++
			if err != nil {
				return err
			}
			if w != nil && emit != nil {
				if err := emit(*w); err != nil {
					return err
				}
			}
			continue
		}
		if r.decisions[i].Action != "" && r.opts.Decision != nil {
			r.opts.Decision(r.decisions[i])
		}
	}
	return nil
}

func (r *campaignRunner) runPlanOnly(ctx context.Context) ([]Finding, error) {
	if err := r.deliverPrepared(ctx, nil); err != nil {
		return nil, err
	}
	if err := r.eachMode(func(mv *modeViews) error {
		return mv.views.validateProducers(ctx)
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &TreeDriftError{Transient: err.Error()}
	}
	r.driftedMu.Lock()
	planDrifted := append([]TargetDrift(nil), r.drifted...)
	r.driftedMu.Unlock()
	if len(planDrifted) > 0 {
		return nil, &TreeDriftError{Drifted: planDrifted}
	}
	var planned []Finding
	for i := range r.findings {
		if r.findings[i].Cached || r.findings[i].Skipped != "" {
			planned = append(planned, r.findings[i])
		}
	}
	return planned, nil
}
