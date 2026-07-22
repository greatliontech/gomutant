package gomutant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
}

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
	return &Store{path: path, moduleDir: abs, overlayDir: overlay}, nil
}

// Committable reports whether a finding is portable repo evidence, and
// when it is not, the first reason it must stay machine-local.
func Committable(f Finding, moduleDir string) (bool, string) {
	if f.Dirty {
		return false, "dirty worktree provenance"
	}
	if f.Commit == "" {
		return false, "no commit provenance"
	}
	subjects := append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...)
	for _, ev := range subjects {
		if ev.RuntimeUnverifiable {
			return false, "runtime-unverifiable evidence for " + ev.Symbol
		}
		if ev.RuntimeInputs == "" {
			continue
		}
		paths, err := runtimeinput.Paths(ev.RuntimeInputs, moduleDir)
		if err != nil {
			return false, "unreadable runtime manifest for " + ev.Symbol
		}
		for _, p := range paths {
			if p != moduleDir && !strings.HasPrefix(p, moduleDir+string(filepath.Separator)) {
				return false, "machine-local runtime input " + p
			}
		}
	}
	return true, ""
}

func (s *Store) entryPath(symbol string) string {
	sum := sha256.Sum256([]byte(symbol))
	return filepath.Join(s.overlayDir, hex.EncodeToString(sum[:12])+".json")
}

// loadOverlay reads every overlay entry; a malformed entry is skipped
// with its removal attempted — the overlay is a cache, never a record
// of note.
func (s *Store) loadOverlay(ctx context.Context) ([]Finding, error) {
	entries, err := os.ReadDir(s.overlayDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.overlayDir, entry.Name()))
		if err != nil {
			continue
		}
		findings, err := ParseFindings(data)
		if err != nil || len(findings) != 1 {
			_ = os.Remove(filepath.Join(s.overlayDir, entry.Name()))
			continue
		}
		out = append(out, findings[0])
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
	return os.Rename(tmpPath, s.entryPath(f.Symbol))
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
			ok, _ := Committable(f, s.moduleDir)
			committable[f.Symbol] = ok
			nextSymbols[f.Symbol] = true
		}
		byRepo := make(map[string]Finding, len(repoPrior))
		for _, f := range repoPrior {
			byRepo[f.Symbol] = f
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
// committable record, "local" with the disqualifying reason otherwise.
func (s *Store) Layer(f Finding) (layer, reason string) {
	if ok, why := Committable(f, s.moduleDir); !ok {
		return "local", why
	}
	return "repo", ""
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
		if ok, _ := Committable(f, s.moduleDir); ok {
			repo++
		} else {
			localOnly++
		}
	}
	return repo, localOnly, nil
}
