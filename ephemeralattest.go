package gomutant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/greatliontech/gomutant/internal/gitcmd"
)

// EphemeralAttestation is one reviewed entry of the committed
// ephemeral-equivalence record (REQ-result-ephemeral-attest): a manual
// probe's mutant, judged equivalent by its author, recorded by edit
// digest with the deciding oracle, the reasoning, and the provenance
// the judgment was made under — greppable beside the findings document
// instead of living only in a commit-message paragraph, and
// distinguishable at a glance from an untested survivor.
type EphemeralAttestation struct {
	// EditDigest identifies the probe's effective mutant: a digest
	// over the ordered replacement set (each tree-relative file with
	// its full replacement content), carried on the EphemeralResult
	// the probe returned, so the recorded row names exactly the
	// measured mutant.
	EditDigest string `json:"editDigest"`
	// RawEditDigest is the same identity over the raw replacement
	// bytes, the row's second key: a canonicalizer that renders the
	// same bytes differently on another toolchain moves the canonical
	// digest, never the raw one, so a row never stops matching the
	// bytes it judged.
	RawEditDigest string `json:"rawEditDigest,omitempty"`
	// DigestForm names the bytes the digest was taken over: "" (the
	// canonical form, every row a version-2 record writes) or "raw"
	// (a row a version-1 record keyed on the raw replacement bytes,
	// kept as written and matched by the raw digest).
	DigestForm string   `json:"digestForm,omitempty"`
	Files      []string `json:"files"`
	TestPkg    string   `json:"testPkg"`
	Run        string   `json:"run"`
	Reason     string   `json:"reason"`
	// Commit and Dirty are the judgment's provenance: the HEAD at
	// attest time and whether the replaced files diverged from it. A
	// loop probe on a staged tree records dirty — the commit then
	// names the nearest ancestor, not the judged content — and a tree
	// whose provenance cannot be established records dirty with no
	// commit, fail-closed, so a row never claims a reproducibility it
	// does not have (REQ-result-ephemeral-attest).
	Commit string `json:"commit,omitempty"`
	Dirty  bool   `json:"dirty,omitempty"`
}

type ephemeralAttestationsDocument struct {
	Version      int                    `json:"version"`
	Attestations []EphemeralAttestation `json:"attestations"`
}

// EphemeralAttestationsPathFor is the committed ephemeral-equivalence
// record's home beside a findings document — the review unit is the
// pair, exactly as with the exemption record.
func EphemeralAttestationsPathFor(findingsPath string) string {
	return filepath.Join(filepath.Dir(findingsPath), "ephemeral-attestations.json")
}

// validateEphemeralAttestation is the one entry predicate the load and
// the write share: an entry missing its identity, oracle, or reasoning
// refuses on both paths, so an invalid row can neither be read as
// authority nor written as a poison pill that makes the record
// permanently unloadable.
func validateEphemeralAttestation(a EphemeralAttestation) error {
	if a.EditDigest == "" || len(a.Files) == 0 || a.TestPkg == "" || a.Run == "" || a.Reason == "" {
		return fmt.Errorf("needs editDigest, files, testPkg, run, and reason")
	}
	if a.DigestForm != "" && a.DigestForm != digestFormRaw {
		return fmt.Errorf("has an unknown digest form %q", a.DigestForm)
	}
	// A canonical row without its raw digest has lost the second key
	// the toolchain mitigation rides on; it refuses like any other
	// incomplete row rather than silently keying on one form.
	if a.DigestForm == "" && a.RawEditDigest == "" {
		return fmt.Errorf("needs rawEditDigest beside a canonical editDigest")
	}
	return nil
}

// LoadEphemeralAttestations reads and validates the committed record; a
// missing file is an empty record.
func LoadEphemeralAttestations(path string) ([]EphemeralAttestation, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gomutant: reading ephemeral-attestation record: %w", err)
	}
	var doc ephemeralAttestationsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("gomutant: ephemeral-attestation record %s: %w", path, err)
	}
	switch doc.Version {
	case ephemeralAttestationsVersion:
	case 1:
		// A version-1 record keyed its rows on the raw replacement
		// bytes; the rows are kept as written, their form named, and
		// the next write carries them forward under version 2.
		for i := range doc.Attestations {
			if doc.Attestations[i].DigestForm == "" {
				doc.Attestations[i].DigestForm = digestFormRaw
			}
		}
	default:
		return nil, fmt.Errorf("gomutant: ephemeral-attestation record %s: unsupported version %d", path, doc.Version)
	}
	for i, a := range doc.Attestations {
		if err := validateEphemeralAttestation(a); err != nil {
			return nil, fmt.Errorf("gomutant: ephemeral-attestation record %s: entry %d %s", path, i, err)
		}
	}
	return doc.Attestations, nil
}

// AttestEphemeralEquivalence builds the attestation row for a
// completed probe, refusing every state that is not an exercised full
// survivor: a kill or a mixed killed-some-runs outcome is evidence
// AGAINST equivalence (evidence beats attestation); a negative parent
// profile leaves reach unestablished; and a survivor whose
// exercise state is UNKNOWN (the coverage probe failed) is
// unverifiable — absence of the unexercised label is not evidence of
// exercise. The provenance stamp records commit and dirty over the
// replaced files, fail-closed dirty when it cannot be established.
func AttestEphemeralEquivalence(ctx context.Context, dir string, res *EphemeralResult, reason string) (EphemeralAttestation, error) {
	if err := ValidateAttestationReason(reason); err != nil {
		return EphemeralAttestation{}, err
	}
	if res == nil || res.EditDigest == "" {
		return EphemeralAttestation{}, fmt.Errorf("gomutant: the probe result carries no edit digest; nothing identifiable to attest")
	}
	if res.KilledRuns > 0 {
		return EphemeralAttestation{}, fmt.Errorf("gomutant: the probe killed in %d of %d runs — evidence beats attestation; a killed or mixed mutant is not equivalent", res.KilledRuns, res.Runs)
	}
	if len(res.UnexercisedFiles) > 0 {
		return EphemeralAttestation{}, fmt.Errorf("gomutant: baseline parent coverage did not establish exercise of %s — absence of profile reach is vacuous evidence for equivalence", strings.Join(res.UnexercisedFiles, ", "))
	}
	if res.CoverageUnknown {
		return EphemeralAttestation{}, fmt.Errorf("gomutant: the probe's exercise state is unknown (the coverage probe failed) — an unverifiable survivor attests nothing; re-run the probe")
	}
	att := EphemeralAttestation{
		EditDigest:    res.EditDigest,
		RawEditDigest: res.RawEditDigest,
		Files:         append([]string(nil), res.Files...),
		TestPkg:       res.TestPkg,
		Run:           res.Run,
		Reason:        reason,
		// Fail-closed: no provenance means dirty with no commit — the
		// row never claims a reproducibility it cannot show.
		Dirty: true,
	}
	if err := validateEphemeralAttestation(att); err != nil {
		return EphemeralAttestation{}, fmt.Errorf("gomutant: the probe result cannot attest: %s", err)
	}
	state, err := captureRepositoryStateContext(ctx, dir, false)
	if err != nil || !state.available {
		// Cancellation is never folded into "no provenance": a
		// cancelled attest must not persist a fail-closed row and
		// report success.
		if cerr := ctx.Err(); cerr != nil {
			return EphemeralAttestation{}, cerr
		}
		return att, nil
	}
	if commit, cerr := state.currentCommitContext(ctx); cerr == nil {
		att.Commit = commit
	} else if ctx.Err() != nil {
		return EphemeralAttestation{}, ctx.Err()
	}
	// The judged paths are absolutized and symlink-resolved before the
	// dirty judgment: the shipped faces pass a relative --dir (default
	// "."), and git's toplevel is the physical path, so a literal join
	// would judge every file "outside the repository" and stamp a
	// pristine tree dirty — the clean case unreachable from either
	// face (the alias-keying discipline measurementResidue documents).
	base := dir
	if abs, aerr := filepath.Abs(dir); aerr == nil {
		base = abs
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			base = resolved
		}
	}
	files := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		p := filepath.Join(base, filepath.FromSlash(f))
		if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
			p = resolved
		}
		files = append(files, p)
	}
	if dirty, _, derr := state.pathsDirtyContext(ctx, files); derr == nil {
		att.Dirty = dirty
	} else if ctx.Err() != nil {
		return EphemeralAttestation{}, ctx.Err()
	}
	return att, nil
}

// ephemeralAttestationsVersion is the record format every write
// carries; a version-1 record is read with its rows' digest form named.
const ephemeralAttestationsVersion = 2

// digestFormRaw marks a row keyed on the raw replacement bytes.
const digestFormRaw = "raw"

// ErrAlreadyAttested reports a record already carrying a row for the
// mutant: a second attestation is a re-judgment the caller must ask
// for by name, never a silent replacement (REQ-result-ephemeral-attest).
type ErrAlreadyAttested struct {
	Digest string
	Reason string
}

func (e *ErrAlreadyAttested) Error() string {
	return "gomutant: equivalence " + e.Digest + " is already attested (" + e.Reason + "); re-attest explicitly to replace the row"
}

// keys are the digests a row answers to: its canonical digest unless
// the row is raw-keyed, and its raw digest where it carries one.
func (a EphemeralAttestation) keys() (canonical, raw string) {
	if a.DigestForm == digestFormRaw {
		return "", a.EditDigest
	}
	return a.EditDigest, a.RawEditDigest
}

// matches reports whether the row names the probe's mutant — by the
// canonical digest or by the raw one, the one predicate the reader
// and the writer share.
func (a EphemeralAttestation) matches(res *EphemeralResult) bool {
	return a.matchesDigests(res.EditDigest, res.RawEditDigest)
}

func (a EphemeralAttestation) matchesDigests(canonical, raw string) bool {
	c, r := a.keys()
	return (c != "" && c == canonical) || (r != "" && r == raw)
}

// standingAttestation is the row a probe's mutant stands attested by:
// a row matched by the canonical digest outranks one matched only by
// the raw digest — a version-1 raw row and a canonical row written for
// another spelling of the same mutant can both match one probe, and
// the canonical row is the judgment of the identity the record keys
// on today, so the reason a face shows never depends on digest sort
// order (REQ-result-ephemeral-attest).
func standingAttestation(atts []EphemeralAttestation, canonical, raw string) *EphemeralAttestation {
	var byRaw *EphemeralAttestation
	for i := range atts {
		c, r := atts[i].keys()
		if c != "" && c == canonical {
			return &atts[i]
		}
		if byRaw == nil && r != "" && r == raw {
			byRaw = &atts[i]
		}
	}
	return byRaw
}

// RecordEphemeralAttestation appends the attestation to the record at
// path under the document lock, atomically: a standing row for the same
// mutant — by either digest form — refuses with that row named unless
// replace asks for it, in which case the standing row is superseded;
// the record stays digest-sorted (REQ-result-ephemeral-attest).
func RecordEphemeralAttestation(ctx context.Context, path string, att EphemeralAttestation, replace bool) error {
	// An uncontended flock is granted without consulting ctx, so a
	// cancelled command must refuse here or it writes and reports
	// success — the document writer's own entry guard.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateEphemeralAttestation(att); err != nil {
		return fmt.Errorf("gomutant: ephemeral attestation %s", err)
	}
	release, err := acquireDocumentLock(ctx, path)
	if err != nil {
		return err
	}
	defer release()
	existing, err := LoadEphemeralAttestations(path)
	if err != nil {
		return err
	}
	if standing := standingAttestation(existing, att.EditDigest, att.RawEditDigest); standing != nil && !replace {
		return &ErrAlreadyAttested{Digest: standing.EditDigest, Reason: standing.Reason}
	}
	// A replacement supersedes every row naming the mutant, by either
	// key, so a raw-keyed legacy row never lingers beside its canonical
	// successor.
	kept := make([]EphemeralAttestation, 0, len(existing)+1)
	for _, e := range existing {
		if !e.matchesDigests(att.EditDigest, att.RawEditDigest) {
			kept = append(kept, e)
		}
	}
	kept = append(kept, att)
	sort.Slice(kept, func(i, j int) bool { return kept[i].EditDigest < kept[j].EditDigest })
	data, err := json.MarshalIndent(ephemeralAttestationsDocument{Version: ephemeralAttestationsVersion, Attestations: kept}, "", "  ")
	if err != nil {
		return err
	}
	mode, err := recordFileMode(path)
	if err != nil {
		return err
	}
	return writeRecordFile(ctx, path, append(data, '\n'), mode)
}

// PrunedEphemeralAttestation names one ephemeral-equivalence row a prune
// removed (or would remove, under check), echoing the row and the reason it
// was judged detached (REQ-result-ephemeral-attest, REQ-result-lifecycle).
type PrunedEphemeralAttestation struct {
	Attestation EphemeralAttestation `json:"attestation"`
	Reason      string               `json:"reason"`
}

// RewrittenEphemeralAttestation names one ephemeral-equivalence row whose
// files, oracle package, or test pattern a retarget rewrote (or would
// rewrite, under check) while preserving the row's original producing
// provenance (REQ-result-ephemeral-attest, REQ-result-lifecycle).
type RewrittenEphemeralAttestation struct {
	EditDigest  string   `json:"editDigest"`
	FromFiles   []string `json:"fromFiles,omitempty"`
	ToFiles     []string `json:"toFiles,omitempty"`
	FromTestPkg string   `json:"fromTestPkg,omitempty"`
	ToTestPkg   string   `json:"toTestPkg,omitempty"`
	FromRun     string   `json:"fromRun,omitempty"`
	ToRun       string   `json:"toRun,omitempty"`
}

func pruneEphemeralAttestations(ctx context.Context, t *Tree, findingsPath string, check bool) ([]PrunedEphemeralAttestation, error) {
	if findingsPath == "" {
		return nil, nil
	}
	path := EphemeralAttestationsPathFor(findingsPath)
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		return nil, err
	}
	if len(atts) == 0 {
		return nil, nil
	}
	state, err := captureRepositoryStateContext(ctx, t.dir, false)
	if err != nil {
		return nil, err
	}
	selKey := SelectionKey(t.Selection())
	var removed []PrunedEphemeralAttestation
	var kept []EphemeralAttestation
	for _, a := range atts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		detachReason := ""
		for _, f := range a.Files {
			abs := filepath.Join(t.dir, filepath.FromSlash(f))
			if _, statErr := os.Stat(abs); os.IsNotExist(statErr) {
				detachReason = fmt.Sprintf("file %s no longer exists", f)
				break
			}
		}
		if detachReason == "" && selKey == "" && !t.eng.HasPackage(a.TestPkg) {
			detachReason = fmt.Sprintf("test package %s no longer resolves", a.TestPkg)
		}
		if detachReason == "" && a.Commit != "" && state.available {
			if _, cerr := gitcmd.Output(ctx, state.root, "cat-file", "-e", a.Commit+"^{commit}"); cerr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				detachReason = fmt.Sprintf("provenance commit %s is unreachable", a.Commit)
			}
		}
		if detachReason != "" {
			removed = append(removed, PrunedEphemeralAttestation{Attestation: a, Reason: detachReason})
			continue
		}
		kept = append(kept, a)
	}
	if !check && len(removed) > 0 {
		if err := writeEphemeralAttestationsLocked(ctx, path, kept); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

func relativeDirRename(fromPkg, toPkg string) (oldRel, newRel string, ok bool) {
	if fromPkg == "" || toPkg == "" || fromPkg == toPkg {
		return "", "", false
	}
	fromParts := strings.Split(fromPkg, "/")
	toParts := strings.Split(toPkg, "/")
	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	if common == 0 {
		return "", "", false
	}
	oldRel = strings.Join(fromParts[common:], "/")
	newRel = strings.Join(toParts[common:], "/")
	if oldRel == "" || newRel == "" {
		return "", "", false
	}
	return oldRel, newRel, true
}

func rewriteFileUnderPrefix(file, oldPrefix, newPrefix string) (string, bool) {
	if oldPrefix == "" || newPrefix == "" {
		return file, false
	}
	if file == oldPrefix {
		return newPrefix, true
	}
	if strings.HasPrefix(file, oldPrefix+"/") {
		return newPrefix + file[len(oldPrefix):], true
	}
	return file, false
}

func rewriteRunPattern(run, fromLocal, toLocal string) (string, bool) {
	if fromLocal == "" || toLocal == "" || fromLocal == toLocal {
		return run, false
	}
	switch {
	case run == fromLocal:
		return toLocal, true
	case run == "^"+fromLocal+"$":
		return "^" + toLocal + "$", true
	case strings.HasPrefix(run, fromLocal+"/"):
		return toLocal + run[len(fromLocal):], true
	case strings.HasPrefix(run, "^"+fromLocal+"/"):
		return "^" + toLocal + run[len("^"+fromLocal):], true
	}
	return run, false
}

func retargetEphemeralAttestations(ctx context.Context, t *Tree, findingsPath, from, to string, check bool) ([]RewrittenEphemeralAttestation, error) {
	if findingsPath == "" {
		return nil, nil
	}
	path := EphemeralAttestationsPathFor(findingsPath)
	atts, err := LoadEphemeralAttestations(path)
	if err != nil {
		return nil, err
	}
	if len(atts) == 0 {
		return nil, nil
	}
	fromPkg, fromLocal := retargetPrefixParts(from)
	toPkg, toLocal := retargetPrefixParts(to)
	oldRel, newRel, hasRelRename := relativeDirRename(fromPkg, toPkg)
	var rewritten []RewrittenEphemeralAttestation
	nextAtts := make([]EphemeralAttestation, len(atts))
	for i, a := range atts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := a
		next.Files = slices.Clone(a.Files)
		filesChanged := false
		pkgChanged := false
		runChanged := false
		if fromLocal == "" && toLocal == "" {
			if nextPkg, ok := retargetPackagePath(a.TestPkg, fromPkg, toPkg); ok && nextPkg != a.TestPkg {
				if !t.eng.HasPackage(nextPkg) {
					return nil, fmt.Errorf("ephemeral attestation %s: rewritten test package %s does not resolve in the current tree", a.EditDigest, nextPkg)
				}
				next.TestPkg = nextPkg
				pkgChanged = true
			}
			for fi, f := range next.Files {
				nextFile, ok := f, false
				if hasRelRename {
					nextFile, ok = rewriteFileUnderPrefix(f, oldRel, newRel)
				}
				if !ok {
					nextFile, ok = rewriteFileUnderPrefix(f, fromPkg, toPkg)
				}
				if ok && nextFile != f {
					abs := filepath.Join(t.dir, filepath.FromSlash(nextFile))
					if _, statErr := os.Stat(abs); statErr != nil {
						return nil, fmt.Errorf("ephemeral attestation %s: rewritten file %s does not exist in the current tree", a.EditDigest, nextFile)
					}
					next.Files[fi] = nextFile
					filesChanged = true
				}
			}
		} else if a.TestPkg == fromPkg && fromPkg == toPkg {
			if nextRun, ok := rewriteRunPattern(a.Run, fromLocal, toLocal); ok {
				if err := t.refuseUnselectedRun(ctx, next.TestPkg, nextRun); err != nil {
					return nil, fmt.Errorf("ephemeral attestation %s: rewritten run %s in %s does not resolve: %w", a.EditDigest, nextRun, next.TestPkg, err)
				}
				next.Run = nextRun
				runChanged = true
			}
		}
		if filesChanged || pkgChanged || runChanged {
			r := RewrittenEphemeralAttestation{EditDigest: a.EditDigest}
			if filesChanged {
				r.FromFiles = slices.Clone(a.Files)
				r.ToFiles = slices.Clone(next.Files)
			}
			if pkgChanged {
				r.FromTestPkg = a.TestPkg
				r.ToTestPkg = next.TestPkg
			}
			if runChanged {
				r.FromRun = a.Run
				r.ToRun = next.Run
			}
			rewritten = append(rewritten, r)
		}
		nextAtts[i] = next
	}
	if !check && len(rewritten) > 0 {
		if err := writeEphemeralAttestationsLocked(ctx, path, nextAtts); err != nil {
			return nil, err
		}
	}
	return rewritten, nil
}

func writeEphemeralAttestationsLocked(ctx context.Context, path string, kept []EphemeralAttestation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := acquireDocumentLock(ctx, path)
	if err != nil {
		return err
	}
	defer release()
	if kept == nil {
		kept = []EphemeralAttestation{}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].EditDigest < kept[j].EditDigest })
	data, err := json.MarshalIndent(ephemeralAttestationsDocument{Version: ephemeralAttestationsVersion, Attestations: kept}, "", "  ")
	if err != nil {
		return err
	}
	mode, err := recordFileMode(path)
	if err != nil {
		return err
	}
	return writeRecordFile(ctx, path, append(data, '\n'), mode)
}
