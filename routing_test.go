package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A write's routing names every record it placed, in the layer the
// write judged it into under the lock, with the disqualifying clauses
// of a machine-local record; a skipped record persists nothing and is
// not routed (REQ-result-layers).
func TestUpdateReturnsItsRouting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	docPath := filepath.Join(root, ".gomutant", "findings.json")
	repo := storeFinding("pkg.Repo", nil)
	local := storeFinding("pkg.Local", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "sealed reason"
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeReason = "sealed reason"
	})
	skipped := Finding{Symbol: "pkg.Skipped", Skipped: "no oracle"}
	store, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	routing, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
		return []Finding{repo, local, skipped}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routing) != 2 {
		t.Fatalf("routing = %+v, want the two measured records alone", routing)
	}
	if p := routing["pkg.Repo"]; p.Layer != LayerRepo || len(p.Reasons) != 0 || p.Reason() != "" {
		t.Fatalf("repo placement = %+v", p)
	}
	p := routing["pkg.Local"]
	if p.Layer != LayerLocal || len(p.Reasons) == 0 || p.Reason() != p.Reasons[0] {
		t.Fatalf("local placement = %+v", p)
	}
	// The memoized path answers the same: a second write of the unchanged
	// records routes them identically, reasons included.
	again, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
		return []Finding{repo, local, skipped}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, routing) {
		t.Fatalf("second write routed %+v, the first %+v", again, routing)
	}
	// The write's own decision, not a re-derivation: the routing agrees
	// with what the store serves for the record it wrote.
	if layer, reason := store.Layer(local); layer != LayerLocal || reason != p.Reason() {
		t.Fatalf("store serves %s/%q, the write routed %s/%q", layer, reason, p.Layer, p.Reason())
	}
}

// The run ledger serves the layer the WRITE routed a record to, never
// the store's predicate re-run after the write: a write that routes a
// record machine-local under a reason of its own answers for it,
// whatever the predicate would say (REQ-result-layers,
// REQ-mcp-findings-doc).
func TestRunLedgerServesTheWritesRouting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	docPath := filepath.Join(root, ".gomutant", "findings.json")
	store, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	committable := storeFinding("pkg.Committable", nil)
	if layer, _ := store.Layer(committable); layer != LayerRepo {
		t.Fatalf("the predicate layers the record %s, want repo for a discriminating stub", layer)
	}
	ledger := NewRunLedger(store, nil, "run-routing", false)
	ledger.Update = func(_ context.Context, change func([]Finding) ([]Finding, error)) (Routing, error) {
		if _, err := change(nil); err != nil {
			return nil, err
		}
		return Routing{"pkg.Committable": {Layer: LayerLocal, Reasons: []string{"the write's own reason"}}}, nil
	}
	if err := ledger.Commit(context.Background())(committable); err != nil {
		t.Fatal(err)
	}
	if layer, reason := ledger.Layer(committable); layer != LayerLocal || reason != "the write's own reason" {
		t.Fatalf("ledger serves %s/%q, want the write's routing", layer, reason)
	}
	// A record no write of this ledger routed classifies as the store
	// reads it.
	other := storeFinding("pkg.Other", nil)
	if layer, _ := ledger.Layer(other); layer != LayerRepo {
		t.Fatalf("an unrouted record layered %s, want the store's answer", layer)
	}
	// A write that reports no placement for the record it committed (a
	// seam standing in for the store) leaves the store to answer — never
	// a zero placement, on the ledger's Layer and on the commit callback.
	silent := NewRunLedger(store, nil, "run-silent", false)
	silent.Update = func(_ context.Context, change func([]Finding) ([]Finding, error)) (Routing, error) {
		_, err := change(nil)
		return Routing{}, err
	}
	var told string
	silent.Committed = func(_ Finding, layer string) { told = layer }
	if err := silent.Commit(context.Background())(committable); err != nil {
		t.Fatal(err)
	}
	if layer, reason := silent.Layer(committable); layer != LayerRepo || reason != "" {
		t.Fatalf("a placement-less write served %s/%q, want the store's answer", layer, reason)
	}
	if told != LayerRepo {
		t.Fatalf("the commit callback was told %q, want the store's answer", told)
	}
}

// A standing committed record the final write re-judges into the
// machine-local overlay — its exemption withdrawn between runs — is a
// demotion the run states: the promotion's twin, a row leaving the
// committed document that git only sees when committed
// (REQ-mcp-findings-doc).
func TestRunLedgerCountsAStandingRecordTheWriteDemotes(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	docPath := filepath.Join(root, ".gomutant", "findings.json")
	unverifiable := storeFinding("pkg.Standing", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "sealed reason"
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeReason = "sealed reason"
	})
	ctx := context.Background()
	record := `{"version":1,"exemptions":[{"subject":"pkg.Standing","reason":"sealed reason","rationale":"reviewed"},{"subject":"pkg.StandingTest","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExemptionsPathFor(docPath), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	routing, err := before.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{unverifiable}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if routing["pkg.Standing"].Layer != LayerRepo {
		t.Fatalf("seeded record routed %+v under the exemption, want repo", routing["pkg.Standing"])
	}
	// The exemption is withdrawn between the runs; the next run's write
	// re-judges the record it never measured.
	if err := os.Remove(ExemptionsPathFor(docPath)); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior) != 1 || store.Overlaid("pkg.Standing") {
		t.Fatalf("prior = %d records, overlaid %v; want one standing repo row", len(prior), store.Overlaid("pkg.Standing"))
	}
	ledger := NewRunLedger(store, prior, "run-demote", false)
	outcome, err := ledger.Finish(ctx, nil, []Target{{Symbol: "pkg.Measured"}}, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Demoted != 1 || outcome.Promoted != 0 {
		t.Fatalf("demoted %d, promoted %d; want the standing record demoted", outcome.Demoted, outcome.Promoted)
	}
	// Placement is a read's fact: a fresh store's load finds the record
	// served from the overlay, the committed document without it.
	after, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := after.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if !after.Overlaid("pkg.Standing") {
		t.Fatal("the demoted record is not served from the overlay after the write")
	}
	if got := outcome.DemotedText(); got != "1 record(s) demoted to the machine-local overlay - findings document changed, commit it" {
		t.Fatalf("demoted text %q", got)
	}
	if err := outcome.PersistedRiding(context.Canceled); err == nil || !strings.Contains(err.Error(), "1 record(s) demoted") {
		t.Fatalf("the persisted demotion does not ride an error exit: %v", err)
	}
}
