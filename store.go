package gomutant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
}

// overlayCacheEntry is one overlay file's cached parse, valid while the
// file's stat identity is unchanged.
type overlayCacheEntry struct {
	size    int64
	modTime time.Time
	finding Finding
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
	abs, err := filepath.Abs(moduleDir)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("gomutant: no user cache directory for the local overlay: %w", err)
	}
	key := sha256.Sum256([]byte(abs))
	overlay := filepath.Join(cache, "gomutant", "repos", hex.EncodeToString(key[:12]), "findings")
	// The committed exemption record beside the findings document is
	// the live authority for the portable line's exemption clause
	// (REQ-result-exemptions); a malformed record refuses the store
	// rather than silently classifying without it.
	exemptions, err := LoadExemptions(ExemptionsPathFor(path))
	if err != nil {
		return nil, err
	}
	return &Store{path: path, moduleDir: abs, overlayDir: overlay, exemptions: exemptions, cache: map[string]overlayCacheEntry{}}, nil
}

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
		if ev.RuntimeUnverifiable && !exempted && add("runtime-unverifiable evidence for "+ev.Symbol) {
			return reasons
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
			// A version AHEAD of this reader is a newer binary's record,
			// not corruption: deleting it would silently destroy
			// machine-local evidence (including overlay-resident
			// attestation reasoning) every time a stale long-lived
			// server touches a document an upgraded CLI wrote. Refuse
			// the whole read instead - the same loud restart signal the
			// repo document's parse gives (REQ-result-export).
			if errors.Is(err, ErrVersionAhead) {
				return nil, fmt.Errorf("machine-local overlay %s: %w", name, err)
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
		if repo, err = ParseFindings(data); err != nil {
			return nil, err
		}
	}
	overlay, err := s.loadOverlay(ctx)
	if err != nil {
		return nil, err
	}
	return mergeLayers(repo, overlay), nil
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
	doc, err := Export([]Finding{f})
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
		// The cache must hold what a parse of the written bytes yields, so
		// the never-persisted run metadata is zeroed before warming.
		parsed := cloneFinding(f)
		parsed.Cached, parsed.Skipped = false, ""
		s.mu.Lock()
		s.cache[filepath.Base(s.entryPath(f.Symbol))] = overlayCacheEntry{size: info.Size(), modTime: info.ModTime(), finding: parsed}
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
func (s *Store) Update(ctx context.Context, update func(prior []Finding) ([]Finding, error)) error {
	var next []Finding
	var pruned []string
	committable := map[string]bool{}
	if err := UpdateDocumentContext(ctx, s.path, func(repoPrior []Finding) ([]Finding, error) {
		overlay, err := s.loadOverlay(ctx)
		if err != nil {
			return nil, err
		}
		next, err = update(mergeLayers(repoPrior, overlay))
		if err != nil {
			return nil, err
		}
		nextSymbols := make(map[string]bool, len(next))
		for _, f := range next {
			ok, _ := Committable(f, s.moduleDir, s.exemptions)
			committable[f.Symbol] = ok
			nextSymbols[f.Symbol] = true
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
			if ok, _ := Committable(f, s.moduleDir, s.exemptions); ok {
				byRepo[f.Symbol] = f
			}
		}
		for _, f := range next {
			if committable[f.Symbol] {
				byRepo[f.Symbol] = f
			}
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
	}); err != nil {
		return err
	}
	for _, f := range next {
		if err := ctx.Err(); err != nil {
			return err
		}
		if committable[f.Symbol] {
			if err := os.Remove(s.entryPath(f.Symbol)); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := s.installEntry(f); err != nil {
			return err
		}
	}
	for _, symbol := range pruned {
		if err := os.Remove(s.entryPath(symbol)); err != nil && !os.IsNotExist(err) {
			return err
		}
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
