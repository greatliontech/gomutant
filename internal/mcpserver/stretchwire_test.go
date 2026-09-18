package mcpserver

import (
	"context"
	"strings"
	"sync"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// observeStretches installs the stretch observer for the test and
// returns a reader of the labels recorded so far, in order.
func observeStretches(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var labels []string
	seams.stretchObserver = func(label string) {
		mu.Lock()
		defer mu.Unlock()
		labels = append(labels, label)
	}
	t.Cleanup(func() { seams.stretchObserver = nil })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), labels...)
	}
}

// wantStretchesInOrder fails unless every wanted stretch heads a
// recorded label, in the order given; a tick's tally rides after the
// stretch's name, and other stretches may interleave.
func wantStretchesInOrder(t *testing.T, labels, wants []string) {
	t.Helper()
	at := 0
	for _, label := range labels {
		if at < len(wants) && strings.HasPrefix(label, wants[at]) {
			at++
		}
	}
	if at != len(wants) {
		t.Fatalf("the stretches never named %q in order; recorded %q", wants[at], labels)
	}
}

// A judged findings call names the load's stretch and then the record
// walk's stages under the inspection lead — the same words the CLI's
// cadence prints (REQ-exec-run-status).
func TestToolFindingsNamesTheWalksStretches(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a tree")
	}
	s, _, _ := seededSurvivorServer(t)
	labels := observeStretches(t)
	if _, _, err := s.toolFindings(context.Background(), nil, findingsIn{Judge: true}); err != nil {
		t.Fatal(err)
	}
	// The walk's heartbeat opens on the load's stretch — the last one
	// this call named — and the walk's read follows at once.
	got := labels()
	if len(got) < 3 || got[0] != gomutant.StretchPreparing(loadingEvent) || got[1] != gomutant.StretchPreparing(loadingEvent) || got[2] != gomutant.StretchInspecting("reading 1 record(s)") {
		t.Fatalf("the judged findings call opened on %q; want the load, the walk's seed on the load, then the read", got)
	}
	wantStretchesInOrder(t, got, []string{gomutant.StretchInspecting("reading 1 record(s)"), gomutant.StretchInspecting("judging 1 record(s)")})
	// The default call loads no tree: its heartbeat opens on the
	// preparation and the walk's read follows, nothing named after.
	labels = observeStretches(t)
	if _, _, err := s.toolFindings(context.Background(), nil, findingsIn{}); err != nil {
		t.Fatal(err)
	}
	if got := labels(); len(got) != 2 || got[0] != gomutant.StretchPreparation || got[1] != gomutant.StretchInspecting("reading 1 record(s)") {
		t.Fatalf("the document-read findings call named %q; want the preparation, then the read alone", got)
	}
}

// The discover tool's selection and its target descriptions ride the
// heartbeat under the selection's stretch, after the load's — the
// CLI's cadence's words (REQ-exec-run-status).
func TestToolDiscoverNamesTheSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	s := serverAt(t)
	labels := observeStretches(t)
	if _, out, err := s.toolDiscover(context.Background(), nil, discoverIn{}); err != nil || out.TargetCount == 0 {
		t.Fatalf("discover = %+v, %v", out, err)
	}
	wantStretchesInOrder(t, labels(), []string{gomutant.StretchPreparing(loadingEvent), gomutant.StretchSelecting})
}

// The attest verb's posture judgment names the load and then the
// judged record's stretches under the inspection lead
// (REQ-exec-run-status).
func TestToolAttestNamesTheJudgedRecordsStretches(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a tree")
	}
	s, _, _ := seededSurvivorServer(t)
	labels := observeStretches(t)
	if _, _, err := s.toolAttest(context.Background(), nil, attestIn{Symbol: "example.com/empty.Old", Position: "p.go:1:1", Operator: "zero return", Reason: "equivalent by inspection"}); err != nil {
		t.Fatal(err)
	}
	got := labels()
	if len(got) < 3 || got[0] != gomutant.StretchPreparing(loadingEvent) || got[1] != gomutant.StretchPreparing(loadingEvent) || !strings.HasPrefix(got[2], gomutant.StretchInspecting("")) {
		t.Fatalf("the attest call opened on %q; want the load, the walk's seed on the load, then the record's stages", got)
	}
}
