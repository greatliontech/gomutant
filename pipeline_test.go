package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// TestRunPipelinesPreparationWithExecution pins the pipelined run shape
// (REQ-exec-run-status): each target's own preparation events precede its
// decision, its decision precedes its first dispatched candidate, and a
// prepared target executes while later targets still prepare — proven by the
// first target's candidates dispatching before the second target's decision,
// with the second target's baseline probe deterministically slow (a sleeping
// oracle test) so the ordering is not a scheduling accident.
func TestRunPipelinesPreparationWithExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/pipe\n\ngo 1.26\n",
		"pa/pa.go":      "package pa\n\nfunc F(x int) int {\n\treturn x + 1\n}\n",
		"pa/pa_test.go": "package pa\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n",
		"pb/pb.go":      "package pb\n\nfunc G(x int) int {\n\treturn x + 2\n}\n",
		"pb/pb_test.go": "package pb\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestG(t *testing.T) {\n\ttime.Sleep(2 * time.Second)\n\tif G(1) != 3 {\n\t\tt.Fail()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// One-candidate windows: the first target forms its own window and
	// executes while the second target's slow baseline still probes.
	prev := runWindowCandidates
	runWindowCandidates = 1
	defer func() { runWindowCandidates = prev }()

	var log []string
	seen := map[string]bool{}
	targets := []Target{
		{Symbol: "example.com/pipe/pa.F"},
		{Symbol: "example.com/pipe/pb.G"},
	}
	if _, err := tr.Run(context.Background(), targets, Options{
		Progress: func(e PreparationEvent) {
			log = append(log, fmt.Sprintf("prep %s %s", e.Stage, e.Symbol))
		},
		Decision: func(d RunDecision) {
			log = append(log, "decision "+d.Symbol)
		},
		dispatched: func(symbol string, mi int) {
			if !seen["dispatched "+symbol] {
				seen["dispatched "+symbol] = true
				log = append(log, "dispatched "+symbol)
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	index := func(entry string) int {
		for i, have := range log {
			if have == entry {
				return i
			}
		}
		t.Fatalf("%q missing from run log:\n%s", entry, strings.Join(log, "\n"))
		return -1
	}
	for _, symbol := range []string{"example.com/pipe/pa.F", "example.com/pipe/pb.G"} {
		mutants := index("prep mutants " + symbol)
		baseline := index(fmt.Sprintf("prep baseline %s", symbol))
		decision := index("decision " + symbol)
		dispatched := index("dispatched " + symbol)
		if !(mutants < decision && baseline < decision && decision < dispatched) {
			t.Fatalf("%s sequence violated (mutants %d, baseline %d, decision %d, dispatched %d):\n%s",
				symbol, mutants, baseline, decision, dispatched, strings.Join(log, "\n"))
		}
	}
	// The overlap pin: pa dispatched while pb — its baseline two seconds
	// slow — was still preparing.
	if index("dispatched example.com/pipe/pa.F") > index("decision example.com/pipe/pb.G") {
		t.Fatalf("execution did not overlap preparation:\n%s", strings.Join(log, "\n"))
	}
}

// TestRunJoinsPreparationOnEarlyReturn pins the producer join
// (REQ-exec-run-status's synchronous-callback contract,
// REQ-exec-cancellation's cleanup wait): a run returning early — here via
// cancellation at the first dispatched candidate, while the second target's
// slow baseline still prepares — must not let the preparation goroutine
// fire callbacks after Run returns. The join itself is enforced by
// construction (one deferred cancel-and-wait covering every return path);
// this net catches a removed join only when the producer sits in a
// callback-adjacent gap at return time, so a survival here is weak
// evidence — the construction, not this test, is the guarantee.
func TestRunJoinsPreparationOnEarlyReturn(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/join\n\ngo 1.26\n",
		"pa/pa.go":      "package pa\n\nfunc F(x int) int {\n\treturn x + 1\n}\n",
		"pa/pa_test.go": "package pa\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n",
		"pb/pb.go":      "package pb\n\nfunc G(x int) int {\n\treturn x + 2\n}\n",
		"pb/pb_test.go": "package pb\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestG(t *testing.T) {\n\ttime.Sleep(2 * time.Second)\n\tif G(1) != 3 {\n\t\tt.Fail()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prev := runWindowCandidates
	runWindowCandidates = 1
	defer func() { runWindowCandidates = prev }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	returned := false
	var late []string
	note := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		if returned {
			late = append(late, entry)
		}
	}
	_, err = tr.Run(ctx, []Target{
		{Symbol: "example.com/join/pa.F"},
		{Symbol: "example.com/join/pb.G"},
	}, Options{
		Progress:   func(e PreparationEvent) { note(fmt.Sprintf("prep %s %s", e.Stage, e.Symbol)) },
		Decision:   func(d RunDecision) { note("decision " + d.Symbol) },
		dispatched: func(string, int) { cancel() },
	})
	mu.Lock()
	returned = true
	mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("early-return run error = %v, want context.Canceled", err)
	}
	// Without the join, the preparation goroutine would still be inside
	// pb's two-second baseline and fire its remaining events about now.
	time.Sleep(3 * time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(late) != 0 {
		t.Fatalf("callbacks fired after Run returned: %v", late)
	}
}

// TestRunPreparationFailureKeepsMeasuredWindows pins the error-path
// aggregation (REQ-exec-attribution's abort terms: an abort that discards
// completed measurements is reserved for corrupted orchestration state): a
// later target's failing baseline — deterministically slow, so both earlier
// windows provably execute first — aborts the run with its error, but every
// executed window, the one held for aggregation included, commits before
// the error surfaces. A gathered-but-unexecuted window is just preparation
// and drops without loss.
func TestRunPreparationFailureKeepsMeasuredWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/keep\n\ngo 1.26\n",
		"pa/pa.go":      "package pa\n\nfunc F(x int) int {\n\treturn x + 1\n}\n",
		"pa/pa_test.go": "package pa\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n",
		"pb/pb.go":      "package pb\n\nfunc G(x int) int {\n\treturn x + 2\n}\n",
		"pb/pb_test.go": "package pb\n\nimport \"testing\"\n\nfunc TestG(t *testing.T) {\n\tif G(1) != 3 {\n\t\tt.Fail()\n\t}\n}\n",
		"pc/pc.go":      "package pc\n\nfunc H(x int) int {\n\treturn x + 3\n}\n",
		"pc/pc_test.go": "package pc\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestH(t *testing.T) {\n\ttime.Sleep(20 * time.Second)\n\tt.Fail()\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	prev := runWindowCandidates
	runWindowCandidates = 1
	defer func() { runWindowCandidates = prev }()

	var committed []string
	_, err = tr.Run(context.Background(), []Target{
		{Symbol: "example.com/keep/pa.F"},
		{Symbol: "example.com/keep/pb.G"},
		{Symbol: "example.com/keep/pc.H"},
	}, Options{
		Budget: 1,
		Commit: func(f Finding) error { committed = append(committed, f.Symbol); return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "oracle baseline does not pass") {
		t.Fatalf("run error = %v, want pc's failing baseline surfaced", err)
	}
	if !slices.Contains(committed, "example.com/keep/pa.F") || !slices.Contains(committed, "example.com/keep/pb.G") {
		t.Fatalf("committed = %v, want both measured targets kept despite the later preparation failure", committed)
	}
	if slices.Contains(committed, "example.com/keep/pc.H") {
		t.Fatalf("committed = %v: the unmeasurable target must not commit", committed)
	}
}

// TestGatherWindowBlocksUntilBudgetOrEnd pins the gather's blocking
// semantics: a window keeps waiting for prepared items until its candidate
// budget is met or preparation ends, never returning just what happened to
// be buffered — window boundaries stay a pure function of target order and
// the budget rule (REQ-exec-attribution's window-scoped flip signal).
func TestGatherWindowBlocksUntilBudgetOrEnd(t *testing.T) {
	items := make(chan work)
	go func() {
		items <- work{candidates: make([]engine.Candidate, 2)}
		time.Sleep(500 * time.Millisecond)
		items <- work{candidates: make([]engine.Candidate, 3)}
		items <- work{candidates: make([]engine.Candidate, 9)}
		close(items)
	}()
	window, ok := gatherWindow(items, 5)
	if !ok || len(window) != 2 || len(window[0].candidates) != 2 || len(window[1].candidates) != 3 {
		t.Fatalf("first window = %d items, want the late-arriving item awaited into a two-item window", len(window))
	}
	window, ok = gatherWindow(items, 5)
	if !ok || len(window) != 1 || len(window[0].candidates) != 9 {
		t.Fatalf("second window = %+v, want the single over-budget item alone", len(window))
	}
	if _, ok := gatherWindow(items, 5); ok {
		t.Fatal("drained channel still yielded a window")
	}
}
