package gomutant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// AGAINST equivalence (evidence beats attestation); an unexercised
// survivor is vacuous — no run reached the edit; and a survivor whose
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
		return EphemeralAttestation{}, fmt.Errorf("gomutant: the probe never exercised %s — an unexercised survivor is vacuous evidence for equivalence", strings.Join(res.UnexercisedFiles, ", "))
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ephemeral-attestations-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
