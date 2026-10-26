package gomutant

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

func mergeFindingObservationsContext(ctx context.Context, root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, err
	}
	// The finding union is judged under the oracle evidence env the
	// caller composed for its run — the injected width included —
	// because its children were ingested under it; a raw-env merge
	// would read a width-reading oracle's records as moved and degrade
	// the union (REQ-exec-oracle-parallelism).
	state, err := runtimeinput.Merge(root, env, states...)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return state, nil
	}
	process := fmt.Sprintf("gomutant-finding-merge-%d", findingObservationSequence.Add(1))
	reason := "runtime input observations could not be merged for reuse: " + err.Error()
	// Best-effort naming: the first pair of observations whose path sets
	// differ names the narrowest place to look - the mover, not the
	// bracket root (REQ-result-inspection). A digest-only conflict
	// yields no path delta and the underlying error stands alone.
	for i := 1; i < len(states); i++ {
		if delta := manifestPathDelta(states[0].Manifest, states[i].Manifest, root); len(delta) > 0 {
			reason += "; diverging inputs: " + cappedNameList(delta, "inputs")
			break
		}
	}
	result, incompleteErr := runtimeinput.Incomplete(root, process, reason, env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	for _, input := range states {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if input.Manifest == "" {
			continue
		}
		merged, mergeErr := runtimeinput.Merge(root, env, result, input)
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

// manifestInterner shares one backing string among a run's identical retained
// observation manifests. The digest is a content hash over exactly the entries
// the canonical encoding serializes, so equal digests name byte-identical
// manifest strings and the sharing is observationally invisible; what it
// removes is candidates-times-manifest retention, which has taken a measuring
// process to tens of gigabytes on document-heavy oracles whose mutants observe
// the same input sets.
type manifestInterner struct {
	mu       sync.Mutex
	byDigest map[string]string
}

func (in *manifestInterner) intern(o runtimeinput.Observation) runtimeinput.Observation {
	if o.Manifest == "" || o.Digest == "" {
		return o
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if canonical, ok := in.byDigest[o.Digest]; ok {
		o.Manifest = canonical
		return o
	}
	in.byDigest[o.Digest] = o.Manifest
	return o
}

// completedObservationUnion unions the finding-wide baseline observations with
// the completed candidate observations. A candidate whose process could not
// prove its runtime evidence sound is excluded from the union and returned as
// explicit candidate evidence carrying its incomplete-process reason and
// measured disposition instead (REQ-exec-observation; candidate evidence,
// REQ-result-record). Baseline observations are always finding-wide: an
// incomplete baseline observation leaves the union — and so the finding —
// unverifiable. A non-nil flagged set restricts the walk to those candidate
// indexes (the re-execution splice); nil walks every runnable candidate.
func completedObservationUnion(ctx context.Context, root string, env []string, baselines []runtimeinput.Observation, candidates []engine.Candidate, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, flagged map[int]bool) (runtimeinput.Observation, []CandidateEvidence, error) {
	states := append([]runtimeinput.Observation(nil), baselines...)
	var evidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, nil, err
		}
		if flagged != nil && !flagged[mi] {
			continue
		}
		if _, runnable := candidate.Mutant(); !runnable {
			continue
		}
		if reason := incompletes[mi]; reason != "" {
			evidence = append(evidence, CandidateEvidence{
				Position:    candidate.Position,
				Operator:    candidate.Operator,
				Reason:      reason,
				Disposition: outcomeDisposition(outcomes[mi]),
			})
			continue
		}
		states = append(states, observations[mi])
	}
	union, err := mergeFindingObservationsContext(ctx, root, env, states...)
	if err != nil {
		return runtimeinput.Observation{}, nil, err
	}
	return union, evidence, nil
}

func outcomeDisposition(outcome engine.MutantOutcome) string {
	switch outcome {
	case engine.MutantKilled:
		return "killed"
	case engine.MutantSurvived:
		return "survived"
	default:
		return "discarded"
	}
}

// emitOracleGuidance attributes oracle instability for a measured finding
// whose merged runtime evidence landed unverifiable under a package-derived
// oracle (REQ-exec-oracle-guidance). Targets sharing one oracle set share one
// attribution: the probes run once per set, not per finding. A budget
// extension owes the same attribution as a whole measure — its suffix
// processes are what landed the merged record unverifiable.
func (t *Tree) emitOracleGuidance(ctx context.Context, f Finding, w work, symbol string, opts runOptions, runEnv []string, guidanceCache map[string]oracleAttribution) error {
	if opts.Guidance == nil || !unstableForBuckets(&f, opts.Exemptions) || f.OracleExplicit {
		return nil
	}
	key := strings.Join(slices.Sorted(slices.Values(w.oracle)), "\x00")
	attr, ok := guidanceCache[key]
	if !ok {
		probed, gerr := t.probeOracleInstability(ctx, w.oracle, w.groups, opts, runEnv)
		if gerr != nil {
			return gerr
		}
		attr = probed
		guidanceCache[key] = attr
	}
	// Best-effort narrowing: module-local inputs the finding observed
	// that no completed solo probe reached (REQ-exec-oracle-guidance).
	var mutantOnly []string
	if attr.completed > 0 {
		if paths, pok := moduleRelInputs(f.TargetEvidence.RuntimeInputs, t.dir); pok {
			mutantOnly = mutantOnlyInputs(paths, attr.probedPaths)
		}
	}
	opts.Guidance(buildOracleGuidance(symbol, f.TargetEvidence.RuntimeReason, f.TargetEvidence.RuntimeAttribution, w.oracle, attr, mutantOnly))
	return nil
}

// unlistedRefusal is the one spelling of a staged run's refusal over a
// failed historical listing — the fact(s), capped, and why the snapshot
// cannot vouch — with the site's remedy appended (the preparation
// names the unstaged measure; the stamp has none to offer)
// (REQ-exec-quiescence, REQ-result-staged).
func unlistedRefusal(unlisted []string, remedy string) string {
	return cappedJoin(unlisted, 8) + "; the staged snapshot cannot vouch for the target" + remedy
}

// provenancePaths assembles the source-file set provenance is computed
// over: the subject views' source files, their historical package files,
// module selection, and workspace inputs; unlisted names every package
// directory whose historical listing failed (the stamp's fact).
func (t *Tree) provenancePaths(ctx context.Context, repository repositoryState, targetView *subjectView, oracleViews []*subjectView) (paths, unlisted []string, err error) {
	// A shaped target has no target view: provenance spans the oracle
	// views alone, plus the shape's own probed files via the shape
	// digest pin (REQ-target-structural).
	var sourceFiles []string
	if targetView != nil {
		sourceFiles = append(sourceFiles, targetView.sourceFiles...)
	}
	for _, oracleView := range oracleViews {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		sourceFiles = append(sourceFiles, oracleView.sourceFiles...)
	}
	historical, unlisted, err := repository.historicalPackageFilesContext(ctx, sourceFiles)
	if err != nil {
		return nil, nil, err
	}
	sourceFiles = append(sourceFiles, historical...)
	sourceFiles = withModuleSelectionPaths(sourceFiles)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return append(sourceFiles, filepath.Join(t.dir, "go.work"), filepath.Join(t.dir, "go.work.sum")), unlisted, nil
}

// externalInputs names the target's provenance inputs outside the
// repository — compile inputs the index snapshot can never vouch for
// (REQ-result-staged), judged over their physical form exactly as the
// dirty stamp judges them — so a staged run refuses the target at
// preparation, before any proof or probe, with the input named
// instead of a drift headline.
func (t *Tree) externalInputs(ctx context.Context, repository repositoryState, targetView *subjectView, oracleViews []*subjectView, shapedFiles []string) (external, unlisted []string, err error) {
	paths, unlisted, err := t.provenancePaths(ctx, repository, targetView, oracleViews)
	if err != nil {
		return nil, nil, err
	}
	paths = append(paths, shapedFiles...)
	seen := map[string]bool{}
	for _, path := range paths {
		if _, kind := repository.placement(path); kind.outside() && !seen[path] {
			seen[path] = true
			external = append(external, path)
		}
	}
	sort.Strings(external)
	return external, unlisted, nil
}

// stampProvenance records the current tree's provenance on a finding -
// measured, drifted, or served: the capture commit, and dirty
// computed over the subject source files, their historical package
// files, module selection, workspace inputs, shapedFiles - a shaped
// target's probed provenance inputs, threaded from the resolve-time
// derivation (a shape that no longer derives refuses at resolve, so no
// stamp-time cannot-derive state exists; nil for symbol targets) - and
// runtime-input paths
// derived from the finding's own subject evidence (attached before any
// stamp on every path). Manifest identities are tree-relative — every
// subject's evidence is anchored at the tree root
// (subjectView.evidenceDir) — so each manifest resolves at the tree; a
// record from before the anchor carries its member base and resolves
// there, never at a root that would materialize local-but-nonexistent
// paths whose dirtiness git never reports and let a false-clean
// portable row land (REQ-result-stale, REQ-result-layers). A record
// measured under dirty
// provenance becomes portable the first time it stamps with those paths
// clean, its attestations riding the promotion. An unreadable evidence
// manifest, or evidence naming a subject no view carries, stamps dirty
// terminally - later evidence goes unexamined, fail-closed.
// The stagedDrift return is non-empty exactly when a staged run's
// snapshot cannot vouch for the target - unstaged drift over its
// measured inputs, or an index re-staged mid-run - and the caller
// routes the target to its drift refusal instead of persisting a
// record (REQ-result-staged); non-staged runs always return "".
func (t *Tree) stampProvenance(ctx context.Context, repository repositoryState, targetView *subjectView, oracleViews []*subjectView, shapedFiles []string, f *Finding) (stagedDrift string, err error) {
	f.StagedTree = repository.stagedTree
	sourceFiles, unlisted, err := t.provenancePaths(ctx, repository, targetView, oracleViews)
	if err != nil {
		return "", err
	}
	if len(unlisted) != 0 && repository.staged {
		// A staged run's records never persist dirty: the failed
		// listing refuses the target with the failure named
		// (REQ-exec-quiescence, REQ-result-staged).
		return unlistedRefusal(unlisted, ""), nil
	}
	// A shaped finding's probed files are provenance inputs the views
	// cannot name: the candidates' replacement sources — the edited
	// recipe file, the rewritten declaring files — and the linkage's
	// in-tree files ride the stamp, threaded from the resolve-time
	// derivation (one derivation per run; a shape that fails to derive
	// already refused at resolve), so a dirty probed file never stamps
	// clean provenance.
	sourceFiles = append(sourceFiles, shapedFiles...)
	viewEnvs := map[string][]string{}
	if targetView != nil {
		viewEnvs[targetView.symbol] = targetView.env
	}
	for _, oracleView := range oracleViews {
		viewEnvs[oracleView.symbol] = oracleView.env
	}
	var evidenceFaults []string
	for _, evidence := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if evidence.RuntimeInputs == "" {
			continue
		}
		base := ""
		if _, viewed := viewEnvs[evidence.Symbol]; viewed {
			base = evidenceBase(t.dir, evidence)
		}
		if base == "" {
			// Terminal: later evidence goes unexamined, fail-closed. A
			// staged run refuses here — its records never persist dirty
			// (REQ-result-staged) — with the evidence fault named; a
			// worktree run stamps the evidence fault on the record and
			// still judges git provenance separately.
			fault := "an evidence manifest names a subject with no view (" + evidence.Symbol + ")"
			if repository.staged {
				return fault + "; the staged snapshot cannot vouch for it", nil
			}
			evidenceFaults = append(evidenceFaults, fault)
			break
		}
		paths, perr := runtimeinput.Paths(evidence.RuntimeInputs, base)
		if perr != nil {
			fault := "an evidence manifest is unreadable (" + evidence.Symbol + ")"
			if repository.staged {
				return fault + "; the staged snapshot cannot vouch for it", nil
			}
			evidenceFaults = append(evidenceFaults, fault)
			break
		}
		// An identity outside the repository is not git's to vouch for:
		// dirty means git-visible drift, and the portable line already
		// keeps a record with external identities machine-local with the
		// truthful machine-local-input reason. Stamping such records
		// dirty forever would only mislabel why they cannot promote
		// (REQ-result-layers). The judgment is placement's — the one
		// physical-form rule the dirty walk shares: an in-repo identity
		// (an alias form included) joins the pathspec at its physical
		// form or, when it no longer exists, at the first unresolved
		// component under its in-repo ancestor; a resolvable external
		// identity is dropped; an identity missing under an external
		// ancestor, or one whose location cannot be established, is a
		// candidate for the digest vouch below - fail-closed, never
		// silently external. The identity itself is what the dirty walk
		// re-judges (placement is idempotent over it), so a tracked link
		// it traverses reaches git even for an outside identity.
		var unresolvable []string
		for _, p := range paths {
			_, kind := repository.placement(p)
			if kind != placedUnknown {
				sourceFiles = append(sourceFiles, p)
			}
			if kind == placedOutsideMissing || kind == placedUnknown {
				unresolvable = append(unresolvable, p)
			}
		}
		if len(unresolvable) > 0 {
			// An externally rooted identity that cannot be established
			// now may still be exactly as recorded: revalidation that
			// reproduces the evidence whole - state, reason, and digest,
			// the serve precheck's own comparison - proves every
			// identity unchanged since measurement (a swept
			// oracle-scratch path recorded missing is missing still),
			// and an unchanged external identity is not git-visible
			// drift. Anything less keeps the fail-closed arm
			// (REQ-result-layers, REQ-exec-oracle-scratch) — dirty
			// forced with the evidence mismatch as its own named cause,
			// never laundered through the git judgment as a
			// path-shaped fact.
			state, serr := runtimeinput.Current(ctx, evidence.RuntimeInputs, base, viewEnvs[evidence.Symbol])
			if serr != nil || !state.OK ||
				state.Unverifiable != evidence.RuntimeUnverifiable || state.Reason != evidence.RuntimeReason ||
				state.Digest != evidence.RuntimeDigest {
				evidenceFaults = append(evidenceFaults, fmt.Sprintf(
					"recorded runtime-input evidence for %s no longer reproduces over externally rooted identities (%s)",
					evidence.Symbol, strings.Join(unresolvable, ", ")))
			}
		}
	}
	dirty, dirtyCauses, err := repository.pathsDirtyContext(ctx, sourceFiles)
	if err != nil {
		return "", err
	}
	f.Dirty = dirty
	if len(evidenceFaults) != 0 {
		faultText := strings.Join(evidenceFaults, "; ")
		if f.EvidenceFault != "" && f.EvidenceFault != faultText {
			f.EvidenceFault += "; " + faultText
		} else {
			f.EvidenceFault = faultText
		}
	}
	dirtyCauses = append(evidenceFaults, dirtyCauses...)
	// The capture commit is read at stamp time, not served from the
	// run-start snapshot — the record names the repository state the
	// finding's just-validated evidence is true of, so ref motion
	// between measurements changes later stamps and discards nothing —
	// and it is read AFTER the dirty judgment so the pair is atomic
	// under REQ-exec-quiescence's precondition, where the only legal
	// mid-run repository event is a commit: a clean judgment means
	// worktree, index, and HEAD agreed when status ran, and a commit
	// landing between the two reads cannot change worktree bytes, so
	// the later-read commit still carries the judged content. A mid-run
	// git failure resolves to the no-commit-provenance posture — Commit
	// empty, Dirty true, machine-local — fail-safe and target-local,
	// never a campaign abort; a staged run, whose records never persist
	// dirty, refuses the target with the failure named instead
	// (REQ-exec-quiescence, REQ-exec-cancellation, REQ-result-staged).
	if commit, cerr := repository.currentCommitContext(ctx); cerr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if repository.staged {
			return "commit provenance unavailable at stamp time; the staged snapshot cannot be attributed", nil
		}
		f.Commit, f.Dirty = "", true
	} else {
		f.Commit = commit
	}
	if len(unlisted) != 0 {
		// A failed historical listing is a git failure at the stamp:
		// the no-commit-provenance posture, target-local
		// (REQ-exec-quiescence).
		f.Commit, f.Dirty = "", true
	}
	if repository.staged {
		if moved, merr := repository.snapshotMovedContext(ctx); merr != nil {
			return "", merr
		} else if moved {
			return "the index snapshot was re-staged mid-run; the recorded tree no longer names the measured content", nil
		}
		if f.Dirty || len(evidenceFaults) != 0 {
			// The refusal names every differing input (capped, counted):
			// an unnamed refusal on a visually clean tree is
			// indistinguishable from a tool fault — the drift may be an
			// ignored or untracked runtime input plain `git status`
			// never shows.
			return "unstaged drift over the measured package's inputs (" + cappedJoin(dirtyCauses, 8) + "); stage or stash it to pin the snapshot", nil
		}
	}
	return "", nil
}

// cappedJoin renders up to limit items with the remainder counted —
// the refusal-evidence list form (long lists cap counted, never
// silently truncate).
func cappedJoin(items []string, limit int) string {
	if len(items) == 0 {
		return "no differing input identified"
	}
	if len(items) <= limit {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:limit], "; ") + fmt.Sprintf("; and %d more", len(items)-limit)
}

// carryAnchoredAttestations partitions prior dispositions over the
// surviving anchors (REQ-attest-survivor): kept dispositions adopt
// their survivor's site (a grandfathered pre-site disposition anchors
// by position and operator); a position-and-operator match whose site
// moved is a site shed - surfaced, never silent; a disposition whose
// survivor is gone lands in gone, the caller's existing shed
// semantics.
func carryAnchoredAttestations(prior []Attestation, survivors []Survivor) (kept []Attestation, siteSheds []AttestationShed, gone []Attestation) {
	siteOf := make(map[survivorKey]string, len(survivors))
	open := make(map[survivorKey]bool, len(survivors))
	for _, s := range survivors {
		key := survivorKey{s.Position, s.Operator}
		open[key] = true
		siteOf[key] = s.Site
	}
	for _, a := range prior {
		key := survivorKey{a.Position, a.Operator}
		if !open[key] {
			gone = append(gone, a)
			continue
		}
		if a.Site != "" && a.Site != siteOf[key] {
			siteSheds = append(siteSheds, AttestationShed{
				Position: a.Position,
				Operator: a.Operator,
				Reason:   "site content changed under the position - the surviving mutant is not the attested one",
			})
			continue
		}
		a.Site = siteOf[key]
		kept = append(kept, a)
	}
	return kept, siteSheds, gone
}

// carryAnchoredWithdrawn carries withdrawn dispositions whose survivor still
// exists at the same site and has not been re-attested.
func carryAnchoredWithdrawn(prior []WithdrawnAttestation, attested []Attestation, survivors []Survivor) []WithdrawnAttestation {
	if len(prior) == 0 {
		return nil
	}
	siteOf := make(map[survivorKey]string, len(survivors))
	surviving := make(map[survivorKey]bool, len(survivors))
	for _, s := range survivors {
		key := survivorKey{s.Position, s.Operator}
		surviving[key] = true
		siteOf[key] = s.Site
	}
	attSet := make(map[survivorKey]bool, len(attested))
	for _, a := range attested {
		attSet[survivorKey{a.Position, a.Operator}] = true
	}
	var kept []WithdrawnAttestation
	seen := make(map[survivorKey]bool, len(prior))
	for _, w := range prior {
		key := survivorKey{w.Position, w.Operator}
		if !surviving[key] || attSet[key] || seen[key] {
			continue
		}
		if w.Site != "" && w.Site != siteOf[key] {
			continue
		}
		w.Site = siteOf[key]
		seen[key] = true
		kept = append(kept, w)
	}
	return kept
}

func testNoun(n int) string { return countNoun(n, "test") }

func packageNoun(n int) string { return countNoun(n, "package") }

func survivorNoun(n int) string { return countNoun(n, "survivor") }

func killNoun(n int) string { return countNoun(n, "kill") }

// proofAbortError names a canceled campaign's in-flight proof abort:
// the wrap carries the target and its oracle so the failure is
// actionable without re-deriving which subject's view was being built
// (REQ-exec-quiescence's legibility arm), and the join keeps the
// cancellation class inspectable when err is a stored union fault that
// predates the cancellation — the bounded retry is skipped on a
// canceled campaign, so err alone may carry no cancellation.
func proofAbortError(target string, oracle []string, err, ctxErr error) error {
	return fmt.Errorf("freshness proof for target %s (oracle %s): %w", target, strings.Join(oracle, ", "), errors.Join(err, ctxErr))
}

// movedPinAttribution names why a prior finding's pins no longer cover the
// request, via the freshness inspection — the class comes from the
// inspection, never an assumed "stale" (an unverifiable prior is not stale) —
// falling back to the caller's generic note when the inspection cannot
// attribute (REQ-result-stale's decision honesty).
func (t *Tree) movedPinAttribution(ctx context.Context, rec Finding, prebuilt *subjectViewSet, fallback string) string {
	inspection, err := t.inspectFindingStateContext(ctx, rec, prebuilt)
	if err == nil && inspection.State != FindingCurrent && inspection.Reason != "" {
		return string(inspection.State) + ": " + inspection.Reason
	}
	return fallback
}

// applyDisposition folds one disposition into an operator summary: delta +1
// scores a fresh outcome, delta -1 retracts a recorded disposition before its
// replacement is scored. Every counts fold in this file goes through it so
// the disposition vocabulary is written once.
func applyDisposition(summary *OperatorSummary, disposition string, delta int) {
	switch disposition {
	case "killed":
		summary.Killed += delta
	case "survived":
		summary.Survived += delta
	case "discarded":
		summary.Discarded += delta
	}
}

// sumOperatorTotals reconciles a finding's totals from its operator
// summaries (REQ-result-candidate-conservation's per-operator ↔ totals
// equations have one derivation).
func sumOperatorTotals(operators []OperatorSummary) (killed, discarded, survived int) {
	for _, summary := range operators {
		killed += summary.Killed
		discarded += summary.Discarded
		survived += summary.Survived
	}
	return killed, discarded, survived
}

// candidateIdentityIndex maps every candidate identity to its enumeration
// index, refusing a duplicate identity: deterministic re-identification of
// recorded per-candidate state is only sound when identities are unique
// (REQ-result-stale's carve-outs).
func candidateIdentityIndex(candidates []engine.Candidate) (map[survivorKey]int, bool) {
	byIdentity := make(map[survivorKey]int, len(candidates))
	for i, candidate := range candidates {
		key := survivorKey{candidate.Position, candidate.Operator}
		if _, duplicate := byIdentity[key]; duplicate {
			return nil, false
		}
		byIdentity[key] = i
	}
	return byIdentity, true
}

// countNoun counts grammatically for decision reasons.
func countNoun(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", n, singular)
}

func candidateNoun(n int) string { return countNoun(n, "candidate") }

// reidentify pins deterministic regeneration to the record: the
// complete-count pin (an unchanged candidate count and the recorded
// selection length) and the unique candidate-identity index every
// serve carve-out's selection predicate builds on (REQ-result-stale).
// A failed pin means the record cannot be spliced and the target
// re-measures whole.
func reidentify(generation engine.Generation, rec Finding) (map[survivorKey]int, bool) {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) != rec.Generated {
		return nil, false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return nil, false
	}
	return byIdentity, true
}

// flaggedCandidateIndexes deterministically re-identifies a served record's
// flagged candidates and recorded survivors within the regenerated candidate
// set. A record whose identities cannot all be re-identified — a moved
// count, a colliding identity, or a flagged candidate that is no longer
// runnable — cannot be spliced and reports false, sending the whole target
// back to re-measurement (REQ-result-stale).
func flaggedCandidateIndexes(generation engine.Generation, rec Finding) (map[int]bool, bool) {
	byIdentity, ok := reidentify(generation, rec)
	if !ok {
		return nil, false
	}
	for _, survivor := range rec.Survivors {
		if _, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]; !ok {
			return nil, false
		}
	}
	flagged := make(map[int]bool, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		i, ok := byIdentity[survivorKey{evidence.Position, evidence.Operator}]
		if !ok {
			return nil, false
		}
		if _, runnable := generation.Candidates[i].Mutant(); !runnable {
			return nil, false
		}
		flagged[i] = true
	}
	return flagged, true
}

// extendedPrefixStands reports whether a regenerated enumeration under a
// wider budget re-identifies a capped record's measured prefix: the complete
// candidate count is unchanged, the selection is strictly longer than the
// record's prefix, every candidate identity is unique, the recorded
// per-operator selected counts equal the prefix's, and every recorded
// survivor re-identifies inside the prefix. Any failure refuses the extension
// so the whole target re-measures (REQ-result-stale's budget-extension
// carve-out).
func extendedPrefixStands(generation engine.Generation, rec Finding) bool {
	if generation.CandidateCount != rec.CandidateCount || len(generation.Candidates) <= rec.Generated {
		return false
	}
	byIdentity, unique := candidateIdentityIndex(generation.Candidates)
	if !unique {
		return false
	}
	prefixOperators := map[string]int{}
	for _, candidate := range generation.Candidates[:rec.Generated] {
		prefixOperators[candidate.Operator]++
	}
	if len(prefixOperators) != len(rec.Operators) {
		return false
	}
	for _, summary := range rec.Operators {
		if prefixOperators[summary.Operator] != summary.Generated {
			return false
		}
	}
	// Attestations always name survivors (enforced at parse), so the
	// survivor walk covers them too.
	for _, survivor := range rec.Survivors {
		i, ok := byIdentity[survivorKey{survivor.Position, survivor.Operator}]
		if !ok || i >= rec.Generated {
			return false
		}
	}
	return true
}

// spliceExtendedFinding serves a capped record under a wider budget
// (REQ-mut-budget, REQ-result-stale's budget-extension carve-out): the
// measured prefix keeps its recorded outcomes, dispositions, and attestations
// while each suffix candidate's fresh outcome and evidence are appended,
// conserving candidate accounting over the merged record
// (REQ-result-candidate-conservation). The suffix processes' completed union
// is reconciled with the record's persisted union folded in: a suffix that
// read only inputs the record already pinned leaves the evidence untouched,
// while a read beyond the record's pins is runtime information it never
// pinned, so the extended outcome is preserved but explicitly non-reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) spliceExtendedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, from int, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, scores windowScores, observations []runtimeinput.Observation, incompletes []string, labels []string, budget int, exemptions []Exemption, memoryPin int64, oracleBudget time.Duration, budgetDerived bool) (Finding, error) {
	outcomes := scores.outcomes
	suffix := make(map[int]bool, len(candidates)-from)
	for mi := from; mi < len(candidates); mi++ {
		suffix[mi] = true
	}
	// The recorded union folds into the suffix union before reconciliation,
	// and the extension reports a measurement, never a cached serve.
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, suffix, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, true, false)
	if err != nil {
		return Finding{}, err
	}
	return extendFindingCounts(ctx, spliced.rec, candidates, from, scores, spliced.fresh, budget, exemptions, memoryPin, oracleBudget, budgetDerived)
}

// splicedEvidence is one splice's evidence outcome: the record after
// fail-closed union reconciliation, the re-executed candidates' fresh
// evidence. The current attach results validate the producing views but do
// not replace the historical evidence retained by a partial measurement
// (REQ-exec-observation).
type splicedEvidence struct {
	rec   Finding
	fresh []CandidateEvidence
}

// spliceRecordedEvidence is the shared evidence spine of every record splice
// (flagged-candidate re-execution and budget extension): union their observations,
// optionally fold the record's persisted union in first (comparing "recorded
// pins plus fresh reads" against the recorded pins), reconcile against the
// record fail-closed (applySplicedUnion), attach the fresh union so
// post-execution producer validation re-establishes the observation bracket,
// and advance only an already complete, preserved binding ledger. Missing
// historical binding evidence is never backfilled by a partial measurement.
// Counts folding stays with
// each arm — replacement and append are different truths
// over the same spine.
func (t *Tree) spliceRecordedEvidence(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, reExecuted map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, outcomes []engine.MutantOutcome, observations []runtimeinput.Observation, incompletes []string, labels []string, foldRecorded, cached bool) (splicedEvidence, error) {
	union, freshEvidence, err := completedObservationUnion(ctx, t.dir, env, baselines, candidates, outcomes, observations, incompletes, reExecuted)
	if err != nil {
		return splicedEvidence{}, err
	}
	if foldRecorded {
		union, err = t.foldRecordedUnion(ctx, env, rec, evidenceBase(t.dir, rec.TargetEvidence), union)
		if err != nil {
			return splicedEvidence{}, err
		}
	}
	portable := newPortableUnion(union, env)
	_, rec, err = t.applySplicedUnion(ctx, env, rec, union, portable, evidenceBase(t.dir, rec.TargetEvidence))
	if err != nil {
		return splicedEvidence{}, err
	}
	_, _, err = attachEvidence(targetView, oracleViews, portable)
	if err != nil {
		return splicedEvidence{}, err
	}
	rec.CompartmentLedger = splicedCompartmentLedger(rec.CompartmentLedger, currentLedger)
	rec.Labels = append([]string(nil), labels...)
	rec.Cached = cached
	return splicedEvidence{rec: rec, fresh: freshEvidence}, nil
}

// foldRecordedUnion merges the served record's persisted completed-process
// union into the suffix processes' fresh union, so the extension's
// reconciliation compares "recorded pins plus suffix reads" against the
// recorded pins alone: a suffix that read only inputs the record already
// pinned folds to exactly the persisted union, while a read beyond the
// record's pins survives the fold and diverges. Adoption re-evaluates the
// persisted manifest against the current tree; a moved or unadoptable
// manifest is left out, and the resulting divergence stamps the extended
// finding non-reusable rather than serving unpinned runtime information
// (REQ-result-stale's fail-closed bound).
func (t *Tree) foldRecordedUnion(ctx context.Context, env []string, rec Finding, moduleDir string, union runtimeinput.Observation) (runtimeinput.Observation, error) {
	// Adoption re-evaluates the persisted union's digests; the record was
	// ingested under the oracle evidence env the caller passes, so
	// adopting under a raw env would read a width-reading record as
	// moved and stamp the extension non-reusable
	// (REQ-exec-oracle-parallelism).
	adopted, adoptErr := runtimeinput.Adopt(rec.TargetEvidence.RuntimeInputs, moduleDir, fmt.Sprintf("gomutant-extend-%d", findingObservationSequence.Add(1)), env)
	if adoptErr != nil {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		return union, nil
	}
	// Persisted evidence is relative to the base its record is anchored
	// at (REQ-inputs-relative-identities); the merge world is absolute, so
	// the adopted record converts before it re-enters
	// (REQ-inputs-absolute-identities). An absolute-era record converts
	// as the identity map.
	adopted, adoptErr = runtimeinput.Absolute(adopted, moduleDir, env)
	if adoptErr != nil {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		return union, nil
	}
	return mergeFindingObservationsContext(ctx, t.dir, env, union, adopted)
}

// extendFindingCounts appends each suffix candidate's fresh outcome to the
// record — per operator and in the finding totals — while every prefix
// candidate keeps its recorded disposition, survivor identity, and
// attestation (REQ-result-candidate-conservation; the prefix pins did not
// move, so its attestations ride unchanged per REQ-attest-survivor). Suffix
// survivors are appended in candidate order, the suffix run's candidate
// evidence becomes the record's (an extendable record carries none), and the
// budget, generated, and candidate counts record the merged truth.
func extendFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, from int, scores windowScores, freshEvidence []CandidateEvidence, budget int, exemptions []Exemption, memoryPin int64, oracleBudget time.Duration, budgetDerived bool) (Finding, error) {
	rec = // The pin is the ENTRY-resolved one the serve gate compared,
		// never a live read of the process global: a mid-campaign change
		// must not ratchet the record (see the run-entry resolution).
		mergeScoredFacts(rec, scores, memoryPin, oracleBudget, budgetDerived)
	outcomes, killers, flips := scores.outcomes, scores.killers, scores.flips
	// Prefix kill attributions carry verbatim — their pins did not move —
	// and suffix kills append theirs; a record predating attribution stays
	// without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	kills := append([]Kill(nil), rec.Kills...)
	operators := append([]OperatorSummary(nil), rec.Operators...)
	byOperator := make(map[string]int, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = i
	}
	survivors := append([]Survivor(nil), rec.Survivors...)
	// Suffix survivors of a divergence-stamped extension classify
	// unstable-oracle at once — their measurement is what ran under unstable
	// runtime conditions — while carried prefix survivors keep their
	// recorded buckets verbatim; a verifiable extension's suffix survivors
	// stay unbucketed here and earn fresh probes afterwards
	// (REQ-exec-survivor-evidence; advisory, never a pin).
	suffixExecution := ""
	if unstableForBuckets(&rec, exemptions) {
		suffixExecution = "unstable-oracle"
	}
	for mi := from; mi < len(candidates); mi++ {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		candidate := candidates[mi]
		i, ok := byOperator[candidate.Operator]
		if !ok {
			i = len(operators)
			operators = append(operators, OperatorSummary{Operator: candidate.Operator})
			byOperator[candidate.Operator] = i
		}
		operators[i].Generated++
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(&operators[i], disposition, 1)
		switch disposition {
		case "survived":
			survivors = append(survivors, newSurvivor(candidate, suffixExecution, flips, mi))
		case "killed":
			if killsComplete {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
			}
		}
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].Operator < operators[j].Operator })
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Operators = operators
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = freshEvidence
	rec.Budget = budget
	rec.Generated = len(candidates)
	// Prefix dispositions carry verbatim, additionally stamping (adopting)
	// sites onto grandfathered pre-site dispositions - the pre-site window
	// closes at first contact on every carry path (REQ-attest-survivor).
	extendSiteOf := make(map[survivorKey]string, len(survivors))
	for _, survivor := range survivors {
		extendSiteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	for i := range rec.Attested {
		if rec.Attested[i].Site == "" {
			rec.Attested[i].Site = extendSiteOf[survivorKey{rec.Attested[i].Position, rec.Attested[i].Operator}]
		}
	}
	rec.Withdrawn = carryAnchoredWithdrawn(rec.Withdrawn, rec.Attested, rec.Survivors)
	return rec, nil
}

// spliceServedFinding serves a record whose only unverifiable runtime evidence
// is candidate-local: covered candidates keep their recorded outcomes while
// each flagged candidate's fresh outcome and evidence replace its recorded
// ones, conserving per-operator and total candidate accounting
// (REQ-result-stale, REQ-result-candidate-conservation). The fresh completed
// union must agree with the record's completed-process union so the spliced
// evidence covers the re-executed processes without shedding any served
// process's pinned runtime inputs; fresh observations that diverge are runtime
// information the record never pinned, so the spliced outcome is preserved but
// explicitly non-reusable (REQ-exec-observation).
func (t *Tree) spliceServedFinding(ctx context.Context, env []string, rec Finding, candidates []engine.Candidate, flagged map[int]bool, baselines []runtimeinput.Observation, targetView *subjectView, oracleViews []*subjectView, currentLedger gofresh.TestVariantLedger, scores windowScores, observations []runtimeinput.Observation, incompletes []string, labels []string, exemptions []Exemption, memoryPin int64, oracleBudget time.Duration, budgetDerived bool) (Finding, error) {
	outcomes := scores.outcomes
	spliced, err := t.spliceRecordedEvidence(ctx, env, rec, candidates, flagged, baselines, targetView, oracleViews, currentLedger, outcomes, observations, incompletes, labels, false, true)
	if err != nil {
		return Finding{}, err
	}
	return spliceFindingCounts(ctx, spliced.rec, candidates, flagged, scores, spliced.fresh, exemptions, memoryPin, oracleBudget, budgetDerived)
}

// applySplicedUnion reconciles the re-executed processes' completed union with
// the served record's persisted union. An equal union leaves the record's
// evidence untouched; a diverged one — different manifest, different digest,
// or an unverifiable fresh state — folds an explicit incomplete observation
// into the union and stamps every subject's evidence with the resulting
// unverifiable state, so the spliced finding is preserved but never reusable
// (REQ-result-stale's fail-closed bound).
func (t *Tree) applySplicedUnion(ctx context.Context, env []string, rec Finding, union runtimeinput.Observation, portable *portableUnion, recordBase string) (runtimeinput.Observation, Finding, error) {
	state, err := runtimeinput.CompletedState(union)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, err
	}
	// The record's union is judged in its own persisted form: relative
	// to the base the record is anchored at (the tree, or a pre-anchor
	// record's member base), or the absolute one an older record
	// carries — either matching means nothing diverged
	// (REQ-inputs-relative-identities).
	relObservation, err := portable.at(recordBase)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
	}
	relState, err := runtimeinput.CompletedState(relObservation)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
	}
	if !splicedUnionDiverged(relState, rec.TargetEvidence) || !splicedUnionDiverged(state, rec.TargetEvidence) {
		return union, rec, nil
	}
	if !state.Unverifiable {
		divergedReason := "runtime input observations diverged from the served record's completed-process union"
		// Best-effort naming: the paths present in exactly one of the two
		// unions are the narrowest place to look (REQ-result-inspection);
		// a digest-only divergence names no path and the reason stands
		// alone.
		if delta := manifestPathDelta(rec.TargetEvidence.RuntimeInputs, state.Manifest, t.dir); len(delta) > 0 {
			divergedReason += "; diverging inputs: " + cappedNameList(delta, "inputs")
		}
		incomplete, incompleteErr := runtimeinput.Incomplete(t.dir, fmt.Sprintf("gomutant-splice-%d", findingObservationSequence.Add(1)), divergedReason, env)
		if incompleteErr != nil {
			return runtimeinput.Observation{}, Finding{}, incompleteErr
		}
		if union, err = mergeFindingObservationsContext(ctx, t.dir, env, union, incomplete); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
		if state, err = runtimeinput.CompletedState(union); err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
	}
	// The merged (possibly incomplete-extended) union re-mints its
	// portable form before stamping — never mutated in place, so no
	// caller holds a stale view — and each evidence row stamps at its
	// OWN module base: a workspace sibling's row must not persist a
	// manifest keyed to the target's base
	// (REQ-inputs-relative-identities).
	*portable = *newPortableUnion(union, portable.env)
	stampAt := func(evidence SubjectEvidence) (SubjectEvidence, error) {
		stamped, err := portable.at(evidenceBase(t.dir, evidence))
		if err != nil {
			return SubjectEvidence{}, fmt.Errorf("%w: %w", errEvidenceFinalization, err)
		}
		re, err := completedEvidence(stamped)
		if err != nil {
			return SubjectEvidence{}, err
		}
		return withRuntimeState(evidence, re), nil
	}
	rec.TargetEvidence, err = stampAt(rec.TargetEvidence)
	if err != nil {
		return runtimeinput.Observation{}, Finding{}, err
	}
	for i := range rec.OracleEvidence {
		rec.OracleEvidence[i], err = stampAt(rec.OracleEvidence[i])
		if err != nil {
			return runtimeinput.Observation{}, Finding{}, err
		}
	}
	return union, rec, nil
}

// evidenceBase is the directory a subject's recorded manifest is
// anchored at: the tree root, or — for a record from before evidence
// anchored at the tree — the member module base it carries
// (REQ-result-layers).
func evidenceBase(treeDir string, evidence SubjectEvidence) string {
	if evidence.ModuleBase == "" {
		return treeDir
	}
	return filepath.Join(treeDir, filepath.FromSlash(evidence.ModuleBase))
}

// treeRelative is p's slash-form path relative to the tree root, judged
// over every pairing of the root and p as given and as symlink-resolved
// (a repository reached through a symlinked prefix spells its own files
// either way, on either side); a p that does not yet exist resolves
// through its deepest existing ancestor. False when p lies outside the
// tree under every pairing.
func treeRelative(treeDir, p string) (string, bool) {
	bases := []string{treeDir}
	if resolved, err := filepath.EvalSymlinks(treeDir); err == nil && resolved != treeDir {
		bases = append(bases, resolved)
	}
	forms := []string{p}
	if resolved, ok := resolveThroughAncestor(p); ok && resolved != p {
		forms = append(forms, resolved)
	}
	for _, base := range bases {
		for _, form := range forms {
			rel, err := filepath.Rel(base, form)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

// resolveThroughAncestor symlink-resolves p, or — when p does not exist
// — its deepest existing ancestor with the unresolved remainder joined
// back on; false when nothing on the chain resolves.
func resolveThroughAncestor(p string) (string, bool) {
	rest := ""
	for dir := p; ; {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			if rest == "" {
				return resolved, true
			}
			return filepath.Join(resolved, rest), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// moduleRelInputs recovers a manifest's tree-local input paths in
// tree-relative slash form regardless of the manifest's stored form:
// recorded findings persist relative-form entries while engine
// observations are absolutized before they leave the engine, and naming
// must read both. Every naming site recovers against the one tree root
// so the sets it compares share a base (a workspace member's inputs
// carry the member prefix on both sides). Relativization tries the root
// as given and symlink-resolved - whichever form the recorded entries
// carry. Best-effort - ok reports whether the manifest decoded; a
// decodable manifest with no tree-local entries returns an empty set,
// which is a statement, not an absence.
func moduleRelInputs(encoded, treeDir string) (paths []string, ok bool) {
	abs, err := runtimeinput.Paths(encoded, treeDir)
	if err != nil {
		return nil, false
	}
	out := []string{}
	for _, p := range abs {
		if rel, ok := treeRelative(treeDir, p); ok {
			out = append(out, rel)
		}
	}
	return out, true
}

// manifestPathDelta names the tree-local input paths present in
// exactly one of two encoded manifests, sorted; undecodable manifests
// yield nothing - naming is best-effort, the divergence stamp itself
// never depends on it.
func manifestPathDelta(prior, fresh, treeDir string) []string {
	priorPaths, priorOK := moduleRelInputs(prior, treeDir)
	freshPaths, freshOK := moduleRelInputs(fresh, treeDir)
	if !priorOK || !freshOK {
		return nil
	}
	inPrior := make(map[string]bool, len(priorPaths))
	for _, path := range priorPaths {
		inPrior[path] = true
	}
	inFresh := make(map[string]bool, len(freshPaths))
	for _, path := range freshPaths {
		inFresh[path] = true
	}
	var delta []string
	for _, path := range priorPaths {
		if !inFresh[path] {
			delta = append(delta, path)
		}
	}
	for _, path := range freshPaths {
		if !inPrior[path] {
			delta = append(delta, path)
		}
	}
	sort.Strings(delta)
	return delta
}

// splicedUnionDiverged reports whether the re-executed processes' completed
// union no longer equals the served record's persisted union. A diverged
// union makes the spliced finding explicitly non-reusable (REQ-result-stale):
// keeping it reusable would serve kills whose processes read inputs the
// record never pinned — the forbidden flattering direction.
func splicedUnionDiverged(state runtimeinput.State, prior SubjectEvidence) bool {
	return state.Unverifiable || state.Manifest != prior.RuntimeInputs || state.Digest != prior.RuntimeDigest
}

// spliceFindingCounts replaces each flagged candidate's recorded disposition
// with its fresh outcome — per operator and in the finding totals — while
// every covered candidate keeps its recorded one
// (REQ-result-candidate-conservation). Survivor identities are rebuilt in
// candidate order carrying their recorded advisory buckets — exact under the
// pins the serve verified — an attestation rides only a survivor that
// survives again at the same position and operator (REQ-attest-survivor),
// and the fresh candidate evidence replaces the served record's.
func spliceFindingCounts(ctx context.Context, rec Finding, candidates []engine.Candidate, flagged map[int]bool, scores windowScores, freshEvidence []CandidateEvidence, exemptions []Exemption, memoryPin int64, oracleBudget time.Duration, budgetDerived bool) (Finding, error) {
	rec = // The pin is the ENTRY-resolved one the serve gate compared,
		// never a live read of the process global: a mid-campaign change
		// must not ratchet the record (see the run-entry resolution).
		mergeScoredFacts(rec, scores, memoryPin, oracleBudget, budgetDerived)
	outcomes, killers, flips := scores.outcomes, scores.killers, scores.flips
	// Covered kills carry their recorded attribution, flagged candidates
	// that die again record their fresh killer; a record predating
	// attribution stays without one (REQ-core-attributed-kills).
	killsComplete := len(rec.Kills) == rec.Killed
	recordedKills := make(map[survivorKey]string, len(rec.Kills))
	for _, kill := range rec.Kills {
		recordedKills[survivorKey{kill.Position, kill.Operator}] = kill.Killer
	}
	var kills []Kill
	operators := append([]OperatorSummary(nil), rec.Operators...)
	rec.Operators = operators
	byOperator := make(map[string]*OperatorSummary, len(operators))
	for i := range operators {
		byOperator[operators[i].Operator] = &operators[i]
	}
	priorSurvivors := make(map[survivorKey]Survivor, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		priorSurvivors[survivorKey{survivor.Position, survivor.Operator}] = survivor
	}
	priorEvidence := make(map[survivorKey]CandidateEvidence, len(rec.CandidateEvidence))
	for _, evidence := range rec.CandidateEvidence {
		priorEvidence[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	freshByKey := make(map[survivorKey]CandidateEvidence, len(freshEvidence))
	for _, evidence := range freshEvidence {
		freshByKey[survivorKey{evidence.Position, evidence.Operator}] = evidence
	}
	var survivors []Survivor
	var candidateEvidence []CandidateEvidence
	for mi, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Finding{}, err
		}
		key := survivorKey{candidate.Position, candidate.Operator}
		if !flagged[mi] {
			if prior, ok := priorSurvivors[key]; ok {
				// A covered survivor carries its recorded advisory bucket
				// verbatim, like its disposition and attestation
				// (REQ-exec-survivor-evidence).
				survivors = append(survivors, carrySurvivor(candidate, prior))
			}
			if killer, ok := recordedKills[key]; ok {
				kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killer})
			}
			continue
		}
		summary := byOperator[candidate.Operator]
		if summary == nil {
			return Finding{}, fmt.Errorf("gomutant: spliced candidate %s %s has no operator summary", candidate.Position, candidate.Operator)
		}
		applyDisposition(summary, priorEvidence[key].Disposition, -1)
		disposition := outcomeDisposition(outcomes[mi])
		applyDisposition(summary, disposition, 1)
		if disposition == "survived" {
			// A re-executed candidate surviving again keeps its recorded
			// bucket when it has one: the pins the serve verified are
			// exactly what makes the recorded classification still exact.
			// A flip into survival (recorded killed or discarded) has no
			// recorded bucket and stays unbucketed like a probe-refused
			// survivor. Under a divergence-stamped splice the re-executed
			// survivor classifies unstable-oracle instead — its
			// re-execution is what ran under unstable runtime conditions —
			// while covered survivors above keep their recorded buckets
			// verbatim (REQ-exec-survivor-evidence).
			execution := priorSurvivors[key].Execution
			if unstableForBuckets(&rec, exemptions) {
				execution = "unstable-oracle"
			}
			survivors = append(survivors, newSurvivor(candidate, execution, flips, mi))
		}
		if disposition == "killed" && killsComplete {
			kills = append(kills, Kill{Position: candidate.Position, Operator: candidate.Operator, Killer: killers[mi]})
		}
		if entry, ok := freshByKey[key]; ok {
			candidateEvidence = append(candidateEvidence, entry)
		}
	}
	for _, summary := range operators {
		if summary.Killed < 0 || summary.Discarded < 0 || summary.Survived < 0 {
			return Finding{}, fmt.Errorf("gomutant: spliced operator %s counts do not reconcile", summary.Operator)
		}
	}
	killed, discarded, survived := sumOperatorTotals(operators)
	rec.Killed = killed
	rec.Discarded = discarded
	rec.Mutants = killed + survived
	rec.Kills = nil
	if killsComplete {
		rec.Kills = kills
	}
	rec.Survivors = survivors
	rec.CandidateEvidence = candidateEvidence
	// A disposition rides only a survivor that survives again at the same
	// position and operator (REQ-attest-survivor).
	current := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		current[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	var kept []Attestation
	for _, attestation := range rec.Attested {
		if current[survivorKey{attestation.Position, attestation.Operator}] {
			kept = append(kept, attestation)
		}
	}
	siteOf := make(map[survivorKey]string, len(rec.Survivors))
	for _, survivor := range rec.Survivors {
		siteOf[survivorKey{survivor.Position, survivor.Operator}] = survivor.Site
	}
	// This carry runs under a gate that pins the mutated source, so the
	// anchor is position and operator over that pinned source; it still
	// stamps (adopts) sites onto grandfathered pre-site dispositions -
	// the pre-site window closes at first contact on every carry path
	// (REQ-attest-survivor). A divergent non-empty site is left for the
	// merge layer, the divergence-surfacing authority.
	for i := range kept {
		if kept[i].Site == "" {
			kept[i].Site = siteOf[survivorKey{kept[i].Position, kept[i].Operator}]
		}
	}
	rec.Attested = kept
	rec.Withdrawn = carryAnchoredWithdrawn(rec.Withdrawn, rec.Attested, rec.Survivors)
	return rec, nil
}

func withRuntimeState(evidence SubjectEvidence, re runtimeEvidence) SubjectEvidence {
	evidence.RuntimeInputs = re.state.Manifest
	evidence.RuntimeDigest = re.state.Digest
	evidence.RuntimeUnverifiable = re.state.Unverifiable
	evidence.RuntimeReason = re.state.Reason
	evidence.RuntimeAttribution = re.attribution
	return evidence
}

func summarizeOperators(candidates []engine.Candidate, outcomes []engine.MutantOutcome) []OperatorSummary {
	byOperator := map[string]*OperatorSummary{}
	operators := make([]string, 0)
	for i, candidate := range candidates {
		summary := byOperator[candidate.Operator]
		if summary == nil {
			summary = &OperatorSummary{Operator: candidate.Operator}
			byOperator[candidate.Operator] = summary
			operators = append(operators, candidate.Operator)
		}
		summary.Generated++
		applyDisposition(summary, outcomeDisposition(outcomes[i]), 1)
	}
	sort.Strings(operators)
	summaries := make([]OperatorSummary, 0, len(operators))
	for _, operator := range operators {
		summaries = append(summaries, *byOperator[operator])
	}
	return summaries
}

// deepestResolvedAncestor walks p's ancestors until one resolves,
// returning the resolved ancestor and the unresolved remainder below
// it. The filesystem root always resolves, so ok is false only for
// degenerate inputs.
func deepestResolvedAncestor(p string) (ancestor, remainder string, ok bool) {
	dir, rest := filepath.Dir(p), filepath.Base(p)
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return resolved, rest, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
