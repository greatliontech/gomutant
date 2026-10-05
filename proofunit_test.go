package gomutant

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
)

// unitLog records a run's proof-unit events in order: each unit's
// priced pass, each target's commit, each unit's release.
type unitLog struct {
	mu     sync.Mutex
	events []string
}

func (l *unitLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *unitLog) index(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

// The observation-proof pass runs per unit — the targets sharing an
// oracle package set — each unit priced with its own subjects and
// packages before it is paid, and released once its last target has
// committed, never before (REQ-exec-analysis-budget,
// REQ-exec-run-status). Two targets over two packages are two units.
func TestProofPassRunsAndReleasesPerUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	log := &unitLog{}
	prior := seams.proofUnitReleased
	seams.proofUnitReleased = func(key string) { log.add("release " + key) }
	t.Cleanup(func() { seams.proofUnitReleased = prior })
	// Each unit's union is derived over ITS members alone — never the
	// mode's whole symbol set: the union seam reports each pass's
	// symbols.
	var unions [][]string
	priorUnion := seams.observedUnion
	seams.observedUnion = func(symbols []string) {
		log.mu.Lock()
		defer log.mu.Unlock()
		unions = append(unions, slices.Clone(symbols))
	}
	t.Cleanup(func() { seams.observedUnion = priorUnion })
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
	}
	var proofs []PreparationEvent
	findings, err := tr.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1,
		Progress: func(e PreparationEvent) {
			if e.Stage == PreparationProofs {
				log.mu.Lock()
				proofs = append(proofs, e)
				log.mu.Unlock()
				log.add("proofs " + e.Text())
			}
		},
		Commit: func(f Finding) error { log.add("commit " + f.Symbol); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 2 {
		t.Fatalf("proof passes priced = %+v; want one per unit, two units", proofs)
	}
	for _, p := range proofs {
		if p.Subjects != 2 || p.Packages != 1 {
			t.Fatalf("a unit's pass priced %+v; want its target and oracle over one package", p)
		}
	}
	wantUnions := [][]string{
		{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"},
		{"example.com/fixture/plain.Ok", "example.com/fixture/plain.TestPlain"},
	}
	if len(unions) != 2 || !slices.Equal(unions[0], wantUnions[0]) || !slices.Equal(unions[1], wantUnions[1]) {
		t.Fatalf("unit unions = %v; want each unit's own members alone, in target order: %v", unions, wantUnions)
	}
	releases := 0
	for _, e := range log.events {
		if strings.HasPrefix(e, "release ") {
			releases++
		}
	}
	if releases != 2 {
		t.Fatalf("units released %d times in %v; want each of the two units once", releases, log.events)
	}
	for _, pkg := range []string{"lib", "plain"} {
		commit := log.index("commit example.com/fixture/" + pkg + ".")
		release := log.index("release ")
		// The unit's release follows its own target's commit: find the
		// release whose key names this unit's oracle package.
		for i, e := range log.events {
			if strings.HasPrefix(e, "release ") && strings.Contains(e, "example.com/fixture/"+pkg) {
				release = i
			}
		}
		if commit < 0 || release < 0 || release < commit {
			t.Fatalf("unit %s: commit at %d, release at %d in %v; want the release after the commit", pkg, commit, release, log.events)
		}
	}
	// The unit loop's release at a CACHE terminal: a warm run serving
	// both records pays no pass and releases each unit at its target's
	// cached decision (REQ-exec-analysis-budget).
	warm := &unitLog{}
	seams.proofUnitReleased = func(key string) { warm.add("release " + key) }
	passes := 0
	cached := 0
	if _, err := tr.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1, Prior: findings,
		Progress: func(e PreparationEvent) {
			if e.Stage == PreparationProofs {
				passes++
			}
		},
		Decision: func(d RunDecision) {
			if d.Action == "cached" {
				cached++
			}
			warm.add("decision " + d.Symbol + " " + d.Action)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if passes != 0 || cached != 2 {
		t.Fatalf("warm run priced %d passes, cached %d targets; want no pass and both served", passes, cached)
	}
	for _, pkg := range []string{"lib", "plain"} {
		decision := warm.index("decision example.com/fixture/" + pkg + ".")
		release := -1
		for i, e := range warm.events {
			if strings.HasPrefix(e, "release ") && strings.Contains(e, "example.com/fixture/"+pkg) {
				release = i
			}
		}
		if decision < 0 || release < 0 || release < decision {
			t.Fatalf("warm unit %s: decision at %d, release at %d in %v; want the release after the cached decision", pkg, decision, release, warm.events)
		}
	}
}

// Targets sharing an oracle package set share one unit: one priced pass
// over their symbols and the oracle, one release after the last of them
// has committed (REQ-exec-analysis-budget).
func TestTargetsSharingAnOraclePackageSetShareOneProofUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	log := &unitLog{}
	prior := seams.proofUnitReleased
	seams.proofUnitReleased = func(key string) { log.add("release " + key) }
	t.Cleanup(func() { seams.proofUnitReleased = prior })
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}
	var proofs []PreparationEvent
	if _, err := tr.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1,
		Progress: func(e PreparationEvent) {
			if e.Stage == PreparationProofs {
				log.mu.Lock()
				proofs = append(proofs, e)
				log.mu.Unlock()
			}
		},
		Commit: func(f Finding) error { log.add("commit " + f.Symbol); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 || proofs[0].Subjects != 3 || proofs[0].Packages != 1 {
		t.Fatalf("proof passes priced = %+v; want one unit over Add, Weak and TestAdd in one package", proofs)
	}
	releaseIdx := slices.IndexFunc(log.events, func(e string) bool { return strings.HasPrefix(e, "release ") })
	lastCommit := -1
	for i, e := range log.events {
		if strings.HasPrefix(e, "commit ") {
			lastCommit = i
		}
	}
	if releaseIdx < 0 || lastCommit < 0 || releaseIdx < lastCommit || strings.Count(strings.Join(log.events, "\n"), "release ") != 1 {
		t.Fatalf("events %v; want one release, after the last of the unit's two commits", log.events)
	}
}
