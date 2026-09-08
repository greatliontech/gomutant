package gomutant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// Store is the two-layer findings persistence (REQ-result-layers): the
// repo document carries only portable, committable records — clean
// provenance, verifiable runtime evidence, no machine-local input
// identities — while everything else lives in a machine-local overlay
// under the user cache directory, keyed by the resolved repo root. A
// read merges both layers with the overlay winning per symbol —
// install-order recency; a wrong winner costs a re-measure, never a
// wrong verdict. A write splits the
// updated set by committability: committable records replace their repo
// rows and delete their overlay entries; local records install
// atomically as per-symbol overlay entries and never touch a repo row
// that still carries portable truth for its own pins.
type Store struct {
	path       string
	moduleDir  string
	overlayDir string
	// exemptions is the committed exemption record loaded from beside
	// the findings document at open (REQ-result-exemptions).
	exemptions []Exemption

	// mu guards the stat-keyed overlay parse cache. The overlay's
	// per-symbol layout makes each entry independently cacheable: a read
	// serves an entry's cached parse while its file's size and mtime are
	// unchanged, so a run's incremental commits re-parse only what moved
	// since the previous read instead of the whole overlay
	// (REQ-result-layers). Cached findings are served as clones — a
	// caller's in-place edit of a merged view must never leak into a
	// later read. The residual stat-key race (an entry replaced with
	// same-size content within mtime granularity) serves a stale parse,
	// which is the overlay's already-tolerated stale-winner shape: it
	// costs a re-measure, never a wrong verdict.
	mu    sync.Mutex
	cache map[string]overlayCacheEntry
	// legacy lists the overlay entries the last read preserved unread:
	// well-formed documents of a version below the reader's range. They
	// are records, not cache — an older binary's attested dispositions
	// live there — so a read names them and never sweeps them
	// (REQ-result-tolerant).
	legacy []LegacyEntry
	// pendingBounds are the coverage bounds a run recorded for the next
	// document write (RecordCoverageBound), keyed by selection: the
	// write merges them over the document's standing rows, the latest
	// run of a selection replacing its row (REQ-result-unreached-bound).
	pendingBounds map[string]CoverageBound
	// judged memoizes each symbol's committability by the persisted
	// form of the record it was judged for: a commit re-judges only
	// the records it changed — the portable-line walk parses every
	// evidence manifest — and a record that changed back to a judged
	// content is served too. Written under the document lock only
	// (Update), like the entries it describes; the exemptions it judges
	// against are fixed for the store's lifetime, so one content judges
	// one way.
	judged map[string]judgedRecord
	// hooks observes the costs the store pays — the test seam for the
	// once-per-content claims.
	hooks storeHooks
	// document is the repo document's cached parse, keyed by the
	// content it parsed (mu guards it). The key is the content itself,
	// not a stat identity, because the document is the merge base of
	// every commit: a stale prior served for a foreign rewrite the key
	// could not distinguish would write the merge back over the foreign
	// rows — a lost record, which the overlay's stale-winner tolerance
	// never covers. Hashing the bytes a read consumes anyway costs a
	// fraction of the parse they would otherwise pay. The store's own
	// writes fill it with the rows they wrote — each a parsed form, so
	// the cache holds exactly what a parse of the file yields
	// (REQ-result-layers). The store holds the parse for its lifetime,
	// the way it holds the overlay's: a long-lived server pays the
	// document's memory once per distinct content, never its parse per
	// call.
	document documentCache
}

// storeHooks are the store's cost observers: walk fires per
// portable-line walk, documentParse per parse of the repo document,
// recordParse per record-sized parse of a changed row. Nil is off.
type storeHooks struct {
	walk          func(symbol string)
	documentParse func()
	recordParse   func(symbol string)
	// beforeSideline runs after a sideline's target path is chosen and
	// before the entry's content is re-checked — the seam for the
	// shared-overlay interleaving in which another store replaced the
	// legacy file between this store's read and its write.
	beforeSideline func(path string)
}

// documentCache is the repo document's parse with the hash of the
// bytes it stands for; a zero value holds nothing.
type documentCache struct {
	sum      [sha256.Size]byte
	findings []Finding
	bounds   []CoverageBound
	held     bool
}

// overlayCacheEntry is one overlay file's cached parse, valid while the
// file's stat identity is unchanged.
type overlayCacheEntry struct {
	size    int64
	modTime time.Time
	finding Finding
}

// judgedRecord is one symbol's memoized committability with the
// persisted form of the record it holds for.
type judgedRecord struct {
	finding     Finding
	committable bool
}

// machineLocalDir derives this machine's per-tree cache home — the
// user cache directory keyed by the resolved tree — shared by the
// findings overlay and the baseline bank so every machine-local
// artifact of one tree lives under one key. It returns the resolved
// absolute module dir alongside.
func machineLocalDir(moduleDir string) (abs, dir string, err error) {
	abs, err = filepath.Abs(moduleDir)
	if err != nil {
		return "", "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", "", fmt.Errorf("gomutant: no user cache directory for machine-local artifacts: %w", err)
	}
	key := sha256.Sum256([]byte(abs))
	return abs, filepath.Join(cache, "gomutant", "repos", hex.EncodeToString(key[:12])), nil
}

// overlayEntryCeiling is the overlay's evidence-size ceiling
// (REQ-result-layers): an entry larger than this is discarded at stat
// time, before any read — orders of magnitude above healthy evidence,
// so only a format regression's residue ever crosses it, and eviction
// costs at most a re-measure.
const overlayEntryCeiling = 64 << 20

// OpenStore opens the two-layer store for the findings document at path
// inside the module rooted at moduleDir.
func OpenStore(path, moduleDir string) (*Store, error) {
	abs, machineDir, err := machineLocalDir(moduleDir)
	if err != nil {
		return nil, err
	}
	overlay := filepath.Join(machineDir, "findings")
	// The committed exemption record beside the findings document is
	// the live authority for the portable line's exemption clause
	// (REQ-result-exemptions); a malformed record refuses the store
	// rather than silently classifying without it.
	exemptions, err := LoadExemptions(ExemptionsPathFor(path))
	if err != nil {
		return nil, err
	}
	return &Store{path: path, moduleDir: abs, overlayDir: overlay, exemptions: exemptions, cache: map[string]overlayCacheEntry{}, judged: map[string]judgedRecord{}}, nil
}

// Exemptions is the exemption record the store opened beside its
// document (REQ-result-exemptions).
func (s *Store) Exemptions() []Exemption { return append([]Exemption(nil), s.exemptions...) }

// portableLineWalk is the one derivation of the portable line
// (REQ-result-layers): dirty or absent commit provenance, each subject
// with runtime-unverifiable evidence or an unreadable runtime manifest,
// and each runtime-input path outside the module directory,
// deduplicated in walk order. stopAtFirst returns after the first
// clause - the write path's committability split needs only the
// verdict, not the full diagnosis.
func portableLineWalk(f Finding, moduleDir string, exemptions []Exemption, stopAtFirst bool) []string {
	var reasons []string
	seen := map[string]bool{}
	add := func(r string) bool {
		if !seen[r] {
			seen[r] = true
			reasons = append(reasons, r)
		}
		return stopAtFirst
	}
	if f.Dirty && add("dirty worktree provenance") {
		return reasons
	}
	if f.Commit == "" && add("no commit provenance") {
		return reasons
	}
	// A reviewed exemption covering every unverifiable subject lifts
	// exactly the unverifiable clause (REQ-result-exemptions); every
	// other portable-line clause still applies.
	_, exempted := coveredExemptions(&f, exemptions)
	subjects := append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...)
	for _, ev := range subjects {
		if ev.RuntimeUnverifiable && !exempted {
			// The recorded gofresh reason names its discharge channel;
			// serving the clause without it would leave the layer
			// disqualifier the one unverifiable answer that dead-ends.
			clause := "runtime-unverifiable evidence for " + ev.Symbol
			if ev.RuntimeReason != "" {
				clause += ": " + ev.RuntimeReason
			}
			if add(clause) {
				return reasons
			}
		}
		if ev.RuntimeInputs == "" {
			continue
		}
		// Each subject's manifest resolves against its own recorded
		// module base: a workspace member's identities live under the
		// member module, and resolving them at the tree root would both
		// mislocate real inputs and misjudge the portable line in either
		// direction. A record without a base resolves at the tree root,
		// the pre-base behavior (REQ-result-layers).
		base := moduleDir
		if ev.ModuleBase != "" {
			base = filepath.Join(moduleDir, filepath.FromSlash(ev.ModuleBase))
		}
		paths, err := runtimeinput.Paths(ev.RuntimeInputs, base)
		if err != nil {
			if add("unreadable runtime manifest for " + ev.Symbol) {
				return reasons
			}
			continue
		}
		for _, p := range paths {
			if p != base && !strings.HasPrefix(p, base+string(filepath.Separator)) {
				if add("machine-local runtime input " + p) {
					return reasons
				}
			}
		}
	}
	return reasons
}

// CommittableReasons lists every portable-line clause a finding fails,
// deduplicated; empty means the record is portable repo evidence. The
// full list exists so a caller repairing one clause is never surprised
// by the next: every single-reason surface derives from the same walk.
func CommittableReasons(f Finding, moduleDir string, exemptions []Exemption) []string {
	return portableLineWalk(f, moduleDir, exemptions, false)
}

// Committable reports whether a finding is portable repo evidence, and
// when it is not, the first reason it must stay machine-local.
func Committable(f Finding, moduleDir string, exemptions []Exemption) (bool, string) {
	if reasons := portableLineWalk(f, moduleDir, exemptions, true); len(reasons) > 0 {
		return false, reasons[0]
	}
	return true, ""
}

func (s *Store) entryPath(symbol string) string {
	sum := sha256.Sum256([]byte(symbol))
	return filepath.Join(s.overlayDir, hex.EncodeToString(sum[:12])+".json")
}

// loadOverlay reads every overlay entry through the stat-keyed parse
// cache; a malformed or over-ceiling entry is skipped with its removal
// attempted — the overlay is a cache, never a record of note, and its
// cost discipline is its content discipline (REQ-result-layers). An
// over-ceiling entry is judged by stat alone, so its bytes are never
// read; the directory listing is the membership authority, so a cached
// parse whose file vanished or changed is dropped, re-parsed, or
// retained unserved (a transient stat failure), never served.
func (s *Store) loadOverlay(ctx context.Context) ([]Finding, error) {
	entries, err := os.ReadDir(s.overlayDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	retained := make(map[string]bool, len(entries))
	var out []Finding
	s.legacy = nil
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := entry.Name()
		path := filepath.Join(s.overlayDir, name)
		// os.Stat, not entry.Info(): the ceiling and the cache key must
		// judge the content a read would consume, so a symlinked entry is
		// sized and keyed by its target, never by the link.
		info, err := os.Stat(path)
		if err != nil {
			// The entry may still exist (transient stat failure); keep its
			// warm parse for the next read rather than sweeping it.
			retained[name] = true
			continue
		}
		if info.Size() > overlayEntryCeiling {
			_ = os.Remove(path)
			continue
		}
		// A sidelined legacy entry is preserved and never served by its
		// NAME, whatever its content: the sideline's rename is
		// check-then-act over a shared directory, so a current record
		// another store installed inside that window could be parked
		// under the name — served, it would be a duplicate row for its
		// symbol that no write ever clears; unserved, it costs one lost
		// measurement, the overlay's tolerated stale-winner shape
		// (REQ-result-layers).
		if version, ok := sidelinedVersion(name); ok {
			s.legacy = append(s.legacy, LegacyEntry{Path: path, Version: version})
			continue
		}
		if cached, ok := s.cache[name]; ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
			retained[name] = true
			out = append(out, cloneFinding(cached.finding))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		findings, err := ParseFindings(data)
		if err != nil || len(findings) != 1 {
			// A document of a version outside the reader's range is
			// another binary's well-formed record, not corruption.
			// AHEAD: deleting it would silently destroy machine-local
			// evidence (including overlay-resident attestation
			// reasoning) every time a stale long-lived server touches a
			// document an upgraded CLI wrote — refuse the whole read,
			// the same loud restart signal the repo document's parse
			// gives (REQ-result-export). BEHIND: its bytes stay — the
			// authored attestation reasoning is unrecoverable by
			// re-measurement — and the read serves nothing from it,
			// naming it for the faces instead (REQ-result-tolerant).
			var versionErr *DocumentVersionError
			if errors.As(err, &versionErr) {
				if versionErr.Sentinel == ErrVersionAhead {
					return nil, fmt.Errorf("machine-local overlay %s: %w", name, err)
				}
				s.legacy = append(s.legacy, LegacyEntry{Path: path, Version: versionErr.Version})
				continue
			}
			_ = os.Remove(path)
			continue
		}
		s.cache[name] = overlayCacheEntry{size: info.Size(), modTime: info.ModTime(), finding: findings[0]}
		retained[name] = true
		out = append(out, cloneFinding(findings[0]))
	}
	for name := range s.cache {
		if !retained[name] {
			delete(s.cache, name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

// LegacyEntry names a machine-local overlay entry the last read
// preserved unread: a well-formed findings document of a version below
// the reader's range, holding an older binary's records — attested
// dispositions included — that no re-measurement can recover
// (REQ-result-tolerant).
type LegacyEntry struct {
	// Path is the entry file, so a reader can export or migrate it.
	Path string
	// Version is the document version the entry declares — for a
	// sidelined entry, the version its NAME declares: the name is the
	// authority there and the content is never read.
	Version int
}

// LegacyEntries returns the overlay entries the most recent read
// preserved unread, in directory order.
func (s *Store) LegacyEntries() []LegacyEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LegacyEntry(nil), s.legacy...)
}

// legacyAt reports the legacy entry the most recent read preserved at
// path, if any.
func (s *Store) legacyAt(path string) (LegacyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.legacy {
		if e.Path == path {
			return e, true
		}
	}
	return LegacyEntry{}, false
}

// sidelinedVersion reports the document version a sidelined legacy
// entry's name declares (`<stem>.legacy-v<version>[-<n>].json`).
func sidelinedVersion(name string) (int, bool) {
	rest, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return 0, false
	}
	_, tail, ok := strings.Cut(rest, ".legacy-v")
	if !ok {
		return 0, false
	}
	version, _, _ := strings.Cut(tail, "-")
	v, err := strconv.Atoi(version)
	if err != nil || v < 0 || strconv.Itoa(v) != version {
		return 0, false
	}
	return v, true
}

// sidelineLegacy moves a preserved legacy entry out of the path a
// current record is about to install at: a fresh measurement of the
// symbol must not overwrite the older binary's authored reasoning
// (REQ-result-layers). The sidelined file keeps the entry suffix so a
// later read preserves and names it exactly as before; the name
// carries the document version and, on a collision, the first free
// ordinal, so two generations of one symbol's legacy records both
// survive. Nothing to do when the path holds no legacy entry.
func (s *Store) sidelineLegacy(path string) error {
	if _, ok := s.legacyAt(path); !ok {
		return nil
	}
	if s.hooks.beforeSideline != nil {
		s.hooks.beforeSideline(path)
	}
	// The legacy view is the read's snapshot and the overlay is shared
	// by every document of the module (only the document lock is held
	// here): another store may have sidelined this entry and installed
	// its current record at the path since. The rename is content-blind,
	// so the content is re-judged first — a file that is no longer a
	// version-behind document of the same version is not sidelined,
	// and the install overwrites it as any current entry.
	// The ceiling is judged by size before any bytes are read, as on
	// the read path; an over-ceiling file is the read path's to evict.
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() > overlayEntryCeiling {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// Still a version-behind document — of whatever version: a
	// different older binary's record that replaced the one the read
	// saw is exactly as worth preserving — and the name carries the
	// content's version, not the snapshot's.
	var versionErr *DocumentVersionError
	if _, perr := ParseFindings(data); !errors.As(perr, &versionErr) || versionErr.Sentinel != ErrVersionBehind {
		return nil
	}
	stem := strings.TrimSuffix(path, ".json")
	for n := 1; ; n++ {
		target := fmt.Sprintf("%s.legacy-v%d.json", stem, versionErr.Version)
		if n > 1 {
			target = fmt.Sprintf("%s.legacy-v%d-%d.json", stem, versionErr.Version, n)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(path, target); err != nil {
			return err
		}
		s.mu.Lock()
		for i := range s.legacy {
			if s.legacy[i].Path == path {
				s.legacy[i].Path, s.legacy[i].Version = target, versionErr.Version
			}
		}
		s.mu.Unlock()
		return nil
	}
}

// LegacyOverlayLine renders the one human line the reading faces print
// for preserved legacy entries: their count, the document versions they
// declare, the range this binary reads, and their directory — so a
// legacy record is never a silent hole (REQ-result-layers). Empty when
// there are none.
func LegacyOverlayLine(entries []LegacyEntry) string {
	if len(entries) == 0 {
		return ""
	}
	seen := map[int]bool{}
	var versions []int
	for _, e := range entries {
		if !seen[e.Version] {
			seen[e.Version] = true
			versions = append(versions, e.Version)
		}
	}
	sort.Ints(versions)
	spelled := make([]string, len(versions))
	for i, v := range versions {
		spelled[i] = strconv.Itoa(v)
	}
	noun := "entries"
	if len(entries) == 1 {
		noun = "entry"
	}
	return fmt.Sprintf("machine-local overlay: %d legacy %s preserved unread (document version %s; this binary reads %d-%d) under %s — an older gomutant wrote them; their attested dispositions are not served",
		len(entries), noun, strings.Join(spelled, ", "), OldestReadableDocumentVersion, DocumentVersion, filepath.Dir(entries[0].Path))
}

// Load merges the repo document with the local overlay, the overlay
// winning per symbol.
func (s *Store) Load(ctx context.Context) ([]Finding, error) {
	data, err := os.ReadFile(s.path)
	var repo []Finding
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		if repo, err = s.readDocument(data); err != nil {
			return nil, err
		}
	}
	overlay, err := s.loadOverlay(ctx)
	if err != nil {
		return nil, err
	}
	return mergeLayers(repo, overlay), nil
}

// readDocument parses the repo document's bytes through the
// content-keyed cache, serving clones — a caller's in-place edit of a
// merged view must never leak into a later read.
func (s *Store) readDocument(data []byte) ([]Finding, error) {
	sum := sha256.Sum256(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.document.held && s.document.sum == sum {
		return cloneFindings(s.document.findings), nil
	}
	if s.hooks.documentParse != nil {
		s.hooks.documentParse()
	}
	doc, err := ParseDocument(data)
	if err != nil {
		return nil, err
	}
	s.document = documentCache{sum: sum, findings: doc.Findings, bounds: doc.CoverageBounds, held: true}
	return cloneFindings(doc.Findings), nil
}

// RecordCoverageBound records a whole-tree run's stated coverage bound
// for the next document write: the bound rides the write that carries
// the run's final merge, replacing the document's row for the same
// selection — an EMPTY bound (the leg reached everything) deleting the
// row, so a standing bound never outlives the run that refuted it
// (REQ-result-unreached-bound). A scoped run never records: its
// population is not the tree's. Not synchronized with a concurrent
// Update: record before the write that should carry it.
func (s *Store) RecordCoverageBound(bound CoverageBound) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingBounds == nil {
		s.pendingBounds = map[string]CoverageBound{}
	}
	s.pendingBounds[bound.Selection] = bound
}

// RecordRunBound is the one seam a run's faces record through: a
// whole-tree run's bound under its declared selection — empty included,
// the record that clears a standing row — rides the write that follows;
// a scoped run or an undeclared selection records nothing
// (REQ-result-unreached-bound). Every face's final merge and every
// zero-target whole-tree reconcile call it, so the rule has one home.
func (s *Store) RecordRunBound(findings []Finding, sel Selection, runID string, wholeTree bool) {
	if !wholeTree {
		return
	}
	if bound := CoverageBoundOf(findings, sel, runID); bound != nil {
		s.RecordCoverageBound(*bound)
	}
}

// CoverageBounds reads the document's stated coverage bounds, one per
// declared selection (REQ-result-unreached-bound); an absent document
// has none.
func (s *Store) CoverageBounds(ctx context.Context) ([]CoverageBound, error) {
	if _, err := s.Load(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.document.bounds), nil
}

// mergedBounds is the document's standing bounds with the pending rows
// laid over them by selection, sorted; read under the lock.
func (s *Store) mergedBounds() []CoverageBound {
	s.mu.Lock()
	defer s.mu.Unlock()
	bySel := map[string]CoverageBound{}
	for _, b := range s.document.bounds {
		bySel[b.Selection] = b
	}
	for sel, b := range s.pendingBounds {
		if len(b.Unreached) == 0 {
			delete(bySel, sel)
			continue
		}
		bySel[sel] = b
	}
	out := make([]CoverageBound, 0, len(bySel))
	for _, b := range bySel {
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b CoverageBound) int { return strings.Compare(a.Selection, b.Selection) })
	return out
}

// cacheDocument records the rows a store write put in the document
// under the hash of the bytes it wrote. Every row is the store's own
// copy — a persisted-form clone of a served record or a fresh parse —
// so no caller holds an alias into the cache.
func (s *Store) cacheDocument(written []byte, rows []Finding, bounds []CoverageBound) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.document = documentCache{sum: sha256.Sum256(written), findings: rows, bounds: bounds, held: true}
}

// cloneFindings clones every record of a slice.
func cloneFindings(findings []Finding) []Finding {
	out := make([]Finding, len(findings))
	for i, f := range findings {
		out[i] = cloneFinding(f)
	}
	return out
}

// mergeLayers merges the two persistence layers, the overlay winning per
// symbol.
func mergeLayers(repo, overlay []Finding) []Finding {
	merged := make(map[string]Finding, len(repo)+len(overlay))
	for _, f := range repo {
		merged[f.Symbol] = f
	}
	for _, f := range overlay {
		merged[f.Symbol] = f
	}
	out := make([]Finding, 0, len(merged))
	for _, f := range merged {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

// installEntry writes one overlay entry atomically.
func (s *Store) installEntry(f Finding) error {
	if err := os.MkdirAll(s.overlayDir, 0o755); err != nil {
		return err
	}
	doc, row, err := persistRecord(f)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.overlayDir, ".entry-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(append(doc, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	// Warm the parse cache from the temp file's stat, taken before the
	// rename so the key names exactly the bytes this install wrote: a
	// run's own commits then never re-parse their own installs, and a
	// concurrent writer's later replacement carries a different stat and
	// re-parses as usual.
	info, statErr := os.Stat(tmpPath)
	if err := os.Rename(tmpPath, s.entryPath(f.Symbol)); err != nil {
		return err
	}
	if statErr == nil {
		// The cache holds what a parse of the written bytes yields — the
		// row the install's own validating parse produced.
		s.mu.Lock()
		s.cache[filepath.Base(s.entryPath(f.Symbol))] = overlayCacheEntry{size: info.Size(), modTime: info.ModTime(), finding: row}
		s.mu.Unlock()
	}
	return nil
}

// Update applies update to the merged layer view and writes the split
// result: committable records to the repo document, the rest to the
// overlay. The caller's update runs inside the repo document's lock
// against the in-lock read merged with the overlay, so membership —
// which rows survive, which symbols prune — is always decided on the
// freshest state and a concurrent session's committed rows are never
// silently evicted; a nested Update on the same document surfaces the
// lock error instead. A repo row is replaced only by a committable
// successor for its symbol, so portable truth is never evicted by a
// local measurement; an overlay entry is deleted the moment its symbol
// gains a committable record. Overlay writes follow the repo write, so
// a crash between them leaves at worst a stale overlay entry shadowing
// the newer repo row — cleared by the symbol's next update, never a
// lost record. The update callback runs under the document lock and
// must not call Store or document methods on the same document — a
// nested writer waits out the lock retries and errors.
// persistedForm is the write path's cheap key for "the same record":
// the never-persisted run metadata (Cached, Skipped) zeroed and every
// list shaped as the encoding round-trips it — an omitted-when-empty
// list absent, a required list present. Equal persisted forms encode
// to equal bytes, so the committability memo and the change comparison
// key by it without a parse, and a parsed row (what the caches hold)
// is a fixed point of it, so a record re-supplied from a merged view
// or in its original in-memory form compares unchanged. Should the
// encoding ever normalize something this key does not mirror, the
// miss costs a needless re-parse or rewrite of one record, never a
// wrong row.
func persistedForm(f Finding) Finding {
	form := cloneFinding(f)
	form.Cached, form.Skipped = false, ""
	form.Labels = absentWhenEmpty(form.Labels)
	form.Kills = absentWhenEmpty(form.Kills)
	form.Survivors = absentWhenEmpty(form.Survivors)
	form.Attested = absentWhenEmpty(form.Attested)
	form.CandidateEvidence = absentWhenEmpty(form.CandidateEvidence)
	form.Exempted = absentWhenEmpty(form.Exempted)
	form.OracleEvidence = presentWhenAbsent(form.OracleEvidence)
	form.Operators = presentWhenAbsent(form.Operators)
	if form.CompartmentLedger != nil {
		form.CompartmentLedger.Declarations = absentWhenEmpty(form.CompartmentLedger.Declarations)
		form.CompartmentLedger.FileHeaders = absentWhenEmpty(form.CompartmentLedger.FileHeaders)
	}
	if form.Shape != nil {
		if form.Shape.Structural != nil {
			form.Shape.Structural.Packages = absentWhenEmpty(form.Shape.Structural.Packages)
		}
		if form.Shape.Manual != nil {
			form.Shape.Manual.Edits = presentWhenAbsent(form.Shape.Manual.Edits)
		}
	}
	return form
}

// absentWhenEmpty is an omitted-when-empty list as a parse yields it.
func absentWhenEmpty[T any](list []T) []T {
	if len(list) == 0 {
		return nil
	}
	return list
}

// presentWhenAbsent is a required list as a parse yields it.
func presentWhenAbsent[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// committable judges a record's committability once per distinct
// persisted content: an unchanged record (the common case across a
// campaign's commits) is served from the memo, never re-walked.
func (s *Store) committable(f, form Finding) bool {
	if memo, ok := s.judged[f.Symbol]; ok && reflect.DeepEqual(memo.finding, form) {
		return memo.committable
	}
	if s.hooks.walk != nil {
		s.hooks.walk(f.Symbol)
	}
	ok, _ := Committable(f, s.moduleDir, s.exemptions)
	s.judged[f.Symbol] = judgedRecord{finding: form, committable: ok}
	return ok
}

// Update applies update to the merged layer view and writes the split
// result: committable records to the repo document, the rest to the
// overlay. The caller's update runs inside the repo document's lock
// against the in-lock read merged with the overlay, so membership —
// which rows survive, which symbols prune — is always decided on the
// freshest state and a concurrent session's committed rows are never
// silently evicted; a nested Update on the same document surfaces the
// lock error instead. A repo row is replaced only by a committable
// successor for its symbol, so portable truth is never evicted by a
// local measurement; an overlay entry is deleted the moment its symbol
// gains a committable record. A commit judges the committability of
// the records it changed and rewrites the overlay entries whose
// persisted form changed — an unchanged record keeps its entry without
// a re-judgment or a rewrite — so a campaign's per-target commits cost
// the changed record, not the document. Overlay writes follow the repo
// write under the same lock, so a crash between them leaves at worst a
// stale overlay entry shadowing the newer repo row — cleared by the
// symbol's next update, never a lost record. The update callback runs
// under the document lock and must not call Store or document methods
// on the same document — a nested writer waits out the lock retries
// and errors.
func (s *Store) Update(ctx context.Context, update func(prior []Finding) ([]Finding, error)) error {
	var next, rows []Finding
	var bounds []CoverageBound
	var pruned []string
	committable := map[string]bool{}
	held := map[string]Finding{}
	forms := map[string]Finding{}
	if err := updateDocument(ctx, s.path, documentUpdate{parse: s.readDocument, update: func(repoPrior []Finding) ([]Finding, error) {
		overlay, err := s.loadOverlay(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range overlay {
			held[f.Symbol] = f
		}
		current := mergeLayers(repoPrior, overlay)
		view := make(map[string]Finding, len(current))
		for _, f := range current {
			view[f.Symbol] = f
		}
		next, err = update(current)
		if err != nil {
			return nil, err
		}
		nextSymbols := make(map[string]bool, len(next))
		for _, f := range next {
			nextSymbols[f.Symbol] = true
			// A skipped record measured nothing: it persists nothing and
			// evicts nothing — the symbol keeps whatever record it holds
			// in whichever layer (the merge rule that nothing-measured
			// never overwrites something-measured, REQ-result-export's
			// exclusion applied per layer).
			if f.Skipped != "" {
				continue
			}
			forms[f.Symbol] = persistedForm(f)
			committable[f.Symbol] = s.committable(f, forms[f.Symbol])
		}
		byRepo := make(map[string]Finding, len(repoPrior))
		for _, f := range repoPrior {
			// "Portable truth is never evicted by a local measurement"
			// protects rows that ARE still portable: an incumbent that
			// fails the current portable line - a revoked exemption is
			// the reachable case - is not portable truth, and retaining
			// it would commit a row the layer contract forbids
			// (REQ-result-layers, REQ-result-exemptions). Its successor
			// lands in whichever layer its own classification earns.
			if form := persistedForm(f); s.committable(f, form) {
				// The store's own copy: the callback holds the view this
				// row came from, and the cache outlives the commit.
				byRepo[f.Symbol] = form
			}
		}
		// Every document row is a parsed form: a record unchanged from
		// the merged view keeps the view's record, which came from a
		// parse — the repo document's, an overlay entry's, or the
		// entry's own validating install; a changed or new record is
		// validated and canonicalized through its own parse, one
		// record's cost. The write below then needs no whole-document
		// self-check: each record's semantics were checked by its parse,
		// the symbol map admits no duplicate, and the interned shape is
		// this binary's own construction.
		for _, f := range next {
			if !committable[f.Symbol] {
				continue
			}
			if prior, held := view[f.Symbol]; held && reflect.DeepEqual(persistedForm(prior), forms[f.Symbol]) {
				byRepo[f.Symbol] = persistedForm(prior)
				continue
			}
			if s.hooks.recordParse != nil {
				s.hooks.recordParse(f.Symbol)
			}
			row, err := parsedForm(f)
			if err != nil {
				return nil, err
			}
			byRepo[f.Symbol] = row
		}
		// A symbol removed from the set entirely (a pruned target)
		// leaves the repo document too, and its overlay entry goes with
		// it: a resurrected local entry would shadow the reconciliation.
		for _, layer := range [][]Finding{repoPrior, overlay} {
			for _, f := range layer {
				if !nextSymbols[f.Symbol] {
					pruned = append(pruned, f.Symbol)
				}
			}
		}
		out := make([]Finding, 0, len(byRepo))
		for symbol, f := range byRepo {
			if !nextSymbols[symbol] {
				continue
			}
			out = append(out, f)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
		return out, nil
	}, export: func(next []Finding) ([]byte, error) {
		bounds = s.mergedBounds()
		data, kept, err := exportDocumentWithBounds(next, bounds)
		rows = kept
		return data, err
	}, after: func(written []byte) error {
		s.cacheDocument(written, rows, bounds)
		s.mu.Lock()
		s.pendingBounds = nil
		s.mu.Unlock()
		// The overlay follows the repo write, under the same lock: an
		// entry is deleted when its record is committable and the
		// overlay holds one, written when its record is not committable
		// and the overlay holds none or a different persisted form, and
		// left alone otherwise.
		for _, f := range next {
			if err := ctx.Err(); err != nil {
				return err
			}
			if f.Skipped != "" {
				continue
			}
			prior, holds := held[f.Symbol]
			switch {
			case committable[f.Symbol]:
				if !holds {
					continue
				}
				if err := os.Remove(s.entryPath(f.Symbol)); err != nil && !os.IsNotExist(err) {
					return err
				}
			case holds && reflect.DeepEqual(prior, forms[f.Symbol]):
				// The overlay already holds this record: nothing to rewrite.
			default:
				if err := s.sidelineLegacy(s.entryPath(f.Symbol)); err != nil {
					return err
				}
				if err := s.installEntry(f); err != nil {
					return err
				}
			}
		}
		for _, symbol := range pruned {
			// A pruned symbol's entry path may hold a legacy entry
			// instead of the pruned record (which lived in the repo
			// document): the legacy file is not the record being
			// pruned and stays (REQ-result-layers).
			if _, legacy := s.legacyAt(s.entryPath(symbol)); legacy {
				continue
			}
			if err := os.Remove(s.entryPath(symbol)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}}); err != nil {
		return err
	}
	return nil
}

// Layer classifies one finding for the findings surfaces: "repo" for a
// committable record, "local" with the first disqualifying reason
// otherwise.
func (s *Store) Layer(f Finding) (layer, reason string) {
	l, reasons := s.LayerReasons(f)
	if l == "local" {
		return l, reasons[0]
	}
	return l, ""
}

// LayerReasons classifies like Layer but carries every failing
// portable-line clause, so a caller repairing one is never surprised by
// the next.
func (s *Store) LayerReasons(f Finding) (layer string, reasons []string) {
	if rs := CommittableReasons(f, s.moduleDir, s.exemptions); len(rs) > 0 {
		return "local", rs
	}
	return "repo", nil
}

// Committability counts the merged view's records per layer for the
// findings surfaces: the repo document is committable by construction,
// and the local-only count says what a reviewer would not inherit.
func (s *Store) Committability(ctx context.Context) (repo, localOnly int, err error) {
	prior, err := s.Load(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, f := range prior {
		if ok, _ := Committable(f, s.moduleDir, s.exemptions); ok {
			repo++
		} else {
			localOnly++
		}
	}
	return repo, localOnly, nil
}

// RollUpMachineLocalInputs collapses machine-local runtime-input
// clauses sharing a top-level directory into one clause naming the
// root and the count - a tempdir-heavy oracle otherwise repeats one
// story per leaked path per subject in every rendered view. Other
// clauses pass through unchanged in place; the full per-path list
// stays derivable from CommittableReasons and the document is
// untouched (REQ-mcp-explain).
func RollUpMachineLocalInputs(reasons []string) []string {
	const prefix = "machine-local runtime input "
	type group struct {
		root  string
		count int
		first int
		one   string
	}
	var order []*group
	byRoot := map[string]*group{}
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		path, ok := strings.CutPrefix(r, prefix)
		if !ok || !strings.HasPrefix(path, "/") {
			out = append(out, r)
			continue
		}
		seg := path[1:]
		if i := strings.IndexByte(seg, '/'); i >= 0 {
			seg = seg[:i]
		}
		root := "/" + seg
		g := byRoot[root]
		if g == nil {
			g = &group{root: root, first: len(out), one: r}
			byRoot[root] = g
			order = append(order, g)
			out = append(out, "")
		}
		g.count++
	}
	for _, g := range order {
		if g.count == 1 {
			out[g.first] = g.one
		} else {
			out[g.first] = fmt.Sprintf("machine-local runtime inputs under %s (%d paths)", g.root, g.count)
		}
	}
	return out
}
