package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

// The two persistence layers a record sits in (REQ-result-layers):
// LayerRepo is the committed findings document, LayerLocal the
// machine-local overlay under the user cache directory.
const (
	LayerRepo  = "repo"
	LayerLocal = "local"
)

// LayerHome names a layer's home the way the faces spell it.
func LayerHome(layer string) string {
	if layer == LayerLocal {
		return "machine-local overlay"
	}
	return "findings document"
}

// RecordCollisionError is a record verb's refusal: after the verb's
// edit two records of one layer would share a symbol, a state the
// layer cannot represent — the document is a symbol map, the overlay
// one entry per symbol — so the verb refuses whole before any write
// (REQ-result-lifecycle). Records of different layers sharing a symbol
// never collide: the overlay shadows the document by design.
type RecordCollisionError struct {
	Symbol string
	Layer  string
}

func (e *RecordCollisionError) Error() string {
	return fmt.Sprintf("%s collides with an existing record in the %s", e.Symbol, LayerHome(e.Layer))
}

// Revision is what a revision leaves behind, the same under check:
// Overlay is the set of symbols the machine-local overlay holds once
// the edits are applied — the verbs' one source for whether the
// overlay shadows a document row (REQ-result-layers).
type Revision struct {
	Overlay map[string]bool
}

// RecordEdit is a record verb's decision for one stored record: the
// record as it should persist and whether it persists at all. It runs
// once per stored record — a symbol held in both layers is two records,
// each edited in its own layer.
type RecordEdit func(layer string, f Finding) (next Finding, keep bool, err error)

// overlayEdit is one change a write makes to the machine-local overlay:
// the removal of every entry found holding symbol, or the install of
// next at its own path. A record renamed in place is two edits — the
// old symbol's removal and the new record's install.
type overlayEdit struct {
	symbol  string
	next    Finding
	removed bool
}

// overlayRemoval is the edit removing every entry found holding symbol.
func overlayRemoval(symbol string) overlayEdit { return overlayEdit{symbol: symbol, removed: true} }

// overlayInstall is the edit installing f at its own path.
func overlayInstall(f Finding) overlayEdit { return overlayEdit{symbol: f.Symbol, next: f} }

// Revise applies edit to every stored record in its own layer, under
// the document lock, and writes exactly the change it decided: the
// repo document is rewritten only when a repo row changed or left, and
// each overlay entry is installed, rewritten, or removed by the name
// the read found it under. It is the record verbs' write — prune and
// retarget act on identity and membership, never on measurement, so a
// record keeps the layer its measuring write placed it in (the merged
// view Update splits by committability is the measuring write's
// shape). A within-layer collision refuses before anything is written;
// under check nothing is written and the edits are judged the same
// way. An overlay failure after the document write names what landed
// (REQ-result-lifecycle, REQ-result-layers).
func (s *Store) Revise(ctx context.Context, check bool, edit RecordEdit) (Revision, error) {
	var (
		repoChanged bool
		edits       []overlayEdit
		rows        []Finding
		bounds      []CoverageBound
		revision    = Revision{Overlay: map[string]bool{}}
	)
	plan := func(repoPrior, overlay []Finding) ([]Finding, error) {
		next := make([]Finding, 0, len(repoPrior))
		seen := make(map[string]bool, len(repoPrior))
		for _, f := range repoPrior {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n, keep, err := edit(LayerRepo, f)
			if err != nil {
				return nil, err
			}
			if !keep {
				repoChanged = true
				continue
			}
			if seen[n.Symbol] {
				return nil, &RecordCollisionError{Symbol: n.Symbol, Layer: LayerRepo}
			}
			seen[n.Symbol] = true
			if n.Symbol == f.Symbol && reflect.DeepEqual(persistedForm(n), persistedForm(f)) {
				// The row as parsed: byte-stable across the rewrite.
				next = append(next, f)
				continue
			}
			repoChanged = true
			row, err := parsedForm(n)
			if err != nil {
				return nil, err
			}
			next = append(next, row)
		}
		sort.Slice(next, func(i, j int) bool { return next[i].Symbol < next[j].Symbol })
		seenLocal := make(map[string]bool, len(overlay))
		for _, f := range overlay {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n, keep, err := edit(LayerLocal, f)
			if err != nil {
				return nil, err
			}
			if !keep {
				edits = append(edits, overlayRemoval(f.Symbol))
				continue
			}
			if seenLocal[n.Symbol] {
				return nil, &RecordCollisionError{Symbol: n.Symbol, Layer: LayerLocal}
			}
			seenLocal[n.Symbol] = true
			revision.Overlay[n.Symbol] = true
			if n.Symbol == f.Symbol && reflect.DeepEqual(persistedForm(n), persistedForm(f)) {
				continue
			}
			if n.Symbol != f.Symbol {
				edits = append(edits, overlayRemoval(f.Symbol))
			}
			edits = append(edits, overlayInstall(n))
		}
		return next, nil
	}
	if check {
		repo, err := s.loadRepo()
		if err != nil {
			return Revision{}, err
		}
		overlay, err := s.loadOverlay(ctx)
		if err != nil {
			return Revision{}, err
		}
		if _, err := plan(repo, overlay); err != nil {
			return Revision{}, err
		}
		// Published as Load publishes it: after the read succeeded whole.
		s.noteOverlaid(overlay)
		return revision, nil
	}
	err := updateDocument(ctx, s.path, documentUpdate{parse: s.readDocument, update: func(repoPrior []Finding) ([]Finding, error) {
		overlay, err := s.loadOverlay(ctx)
		if err != nil {
			return nil, err
		}
		return plan(repoPrior, overlay)
	}, export: func(next []Finding) ([]byte, error) {
		if !repoChanged {
			// No repo row changed: the document stands as it is —
			// a re-emission would be a change the verb never reported.
			return nil, nil
		}
		bounds = s.mergedBounds()
		data, kept, err := renderDocument(next, bounds)
		rows = kept
		return data, err
	}, after: func(written []byte) error {
		if written != nil {
			s.cacheDocument(written, rows, bounds)
			s.mu.Lock()
			s.pendingBounds = nil
			s.mu.Unlock()
		}
		return s.applyOverlayEdits(ctx, written != nil, edits)
	}})
	if err != nil {
		return Revision{}, err
	}
	return revision, nil
}

// applyOverlayEdits is the one overlay writer — the measuring write's
// and a revision's. It lands a set of edits in an order-independent
// way: first every kept record another edit's install would overwrite
// at its own path is re-homed at its own path (a hand-parked entry at
// another symbol's path, and any record parked at THAT record's path,
// to a fixpoint); then every entry a removed record was found under
// goes; then every install lands at its record's own path; then each
// installed symbol's other names go, so a symbol's entries are exactly
// one afterwards. A record this write installs is re-homed like any
// other when it sits parked at a destination — the install lands over
// the re-home — so a failure before its install still leaves its
// last record at its own path. Every removal spares every path this write installs
// at, which the install overwrites — a hand-parked entry at another
// symbol's own path would otherwise take that symbol's fresh record
// with it. A failure names how far the write got: the document's state
// and the count of edits landed (REQ-result-lifecycle,
// REQ-result-layers).
func (s *Store) applyOverlayEdits(ctx context.Context, documentRewritten bool, edits []overlayEdit) error {
	var (
		removals     []string
		installs     []Finding
		destinations = map[string]bool{}
		leaving      = map[string]bool{}
	)
	for _, e := range edits {
		if e.removed {
			removals = append(removals, e.symbol)
			leaving[e.symbol] = true
			continue
		}
		destinations[s.entryPath(e.symbol)] = true
		installs = append(installs, e.next)
	}
	total := len(removals) + len(installs)
	landed := 0
	var rehomed map[string]bool
	partial := func(err error) error {
		state := "the findings document is untouched"
		if documentRewritten {
			state = "the findings document was rewritten"
		}
		rehomes := ""
		if len(rehomed) > 0 {
			rehomes = fmt.Sprintf(" %d parked record(s) re-homed at their own paths;", len(rehomed))
		}
		return fmt.Errorf("%s;%s %d of %d machine-local overlay edits landed before: %w", state, rehomes, landed, total, err)
	}
	rehomed, err := s.rehomeParked(ctx, destinations, leaving)
	if err != nil {
		return partial(err)
	}
	for path := range rehomed {
		destinations[path] = true
	}
	for _, symbol := range removals {
		if err := ctx.Err(); err != nil {
			return partial(err)
		}
		if err := s.removeServed(symbol, destinations); err != nil {
			return partial(err)
		}
		landed++
	}
	for _, f := range installs {
		if err := ctx.Err(); err != nil {
			return partial(err)
		}
		if _, err := s.installAt(f); err != nil {
			return partial(err)
		}
		if err := s.removeServed(f.Symbol, destinations); err != nil {
			return partial(err)
		}
		landed++
	}
	return nil
}

// rehomeParked re-installs, at its own path, every kept record the last
// read found parked at a path this write installs at: without it the
// install would overwrite that record's only entry and the write would
// lose a record it reported nothing about. The record installed is the
// one the read served (its own choice among the symbol's entries) — a
// record this write removes needs no re-home: it is leaving. A re-home
// is itself an install, so a record parked at the re-homed record's
// own path is re-homed in turn, to a fixpoint. The re-homed paths are
// returned so the write's removals spare them.
func (s *Store) rehomeParked(ctx context.Context, destinations, leaving map[string]bool) (map[string]bool, error) {
	s.mu.Lock()
	parked := map[string]string{}
	records := make(map[string]Finding, len(s.servedRecord))
	for symbol, names := range s.served {
		for _, name := range names {
			parked[filepath.Join(s.overlayDir, name)] = symbol
		}
		// Every symbol in served was served from one of its names, so
		// the read's chosen record exists for it.
		records[symbol] = cloneFinding(s.servedRecord[symbol])
	}
	s.mu.Unlock()
	rehomed := map[string]bool{}
	pending := destinations
	for len(pending) > 0 {
		next := map[string]bool{}
		for path := range pending {
			symbol, ok := parked[path]
			if !ok || leaving[symbol] || rehomed[s.entryPath(symbol)] || s.entryPath(symbol) == path {
				continue
			}
			if err := ctx.Err(); err != nil {
				return rehomed, err
			}
			home, err := s.installAt(records[symbol])
			if err != nil {
				return rehomed, err
			}
			rehomed[home] = true
			next[home] = true
		}
		pending = next
	}
	return rehomed, nil
}

// installAt lands f at its own path — a legacy entry parked there moved
// aside first, never overwritten (REQ-result-layers) — and returns the
// path.
func (s *Store) installAt(f Finding) (string, error) {
	home := s.entryPath(f.Symbol)
	if err := s.sidelineLegacy(home); err != nil {
		return home, err
	}
	if err := s.installEntry(f); err != nil {
		return home, err
	}
	return home, nil
}

// removeServed removes every overlay entry the last read found holding
// symbol, except the paths in keep — the write's own install
// destinations — so a write acts on the entries a read finds: an entry
// a hand edit of the overlay left under a foreign name — a textual
// rename of its content — is served by its content and removed by its
// name, never missed at the symbol's hashed path (REQ-result-layers).
// An entry already gone was removed by another session, the overlay's
// tolerated race.
func (s *Store) removeServed(symbol string, keep map[string]bool) error {
	s.mu.Lock()
	names := append([]string(nil), s.served[symbol]...)
	s.mu.Unlock()
	for _, name := range names {
		path := filepath.Join(s.overlayDir, name)
		if keep[path] {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// loadRepo reads the repo document's rows through the content-keyed
// cache; a missing document reads as empty.
func (s *Store) loadRepo() ([]Finding, error) {
	data, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return s.readDocument(data)
}
