package gomutant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ledgerStore(t *testing.T, prior []Finding) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := OpenStore(filepath.Join(root, ".gomutant", "findings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior) > 0 {
		if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) { return prior, nil }); err != nil {
			t.Fatal(err)
		}
	}
	// The read a preparation performs: the store learns where each
	// record sits.
	if _, err := store.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, root
}

// Every shed reaches a face exactly once, the first report winning: a
// site shed as the run reports it, a commit's strip after its update,
// the final merge's residue last — and a mutant whose contradiction
// already told its fate is never retold (REQ-attest-survivor).
func TestRunLedgerReportsEachShedOnce(t *testing.T) {
	att := func(pos string) Attestation {
		return Attestation{Position: pos, Operator: "comparison: > -> >=", Reason: "equivalent", Site: "aaaa1111aaaa1111"}
	}
	surv := func(pos string) Survivor {
		return Survivor{Position: pos, Operator: "comparison: > -> >=", Site: "aaaa1111aaaa1111"}
	}
	attested := func(symbol, pos string) Finding {
		return storeFinding(symbol, func(f *Finding) {
			f.Killed = 0
			f.Survivors = []Survivor{surv(pos)}
			f.Operators = []OperatorSummary{{Operator: "comparison: > -> >=", Generated: 1, Survived: 1}}
			f.Attested = []Attestation{att(pos)}
		})
	}
	// pkg.A's survivor vanishes from the fresh record: its disposition
	// sheds at the commit. pkg.B's does too, but its contradiction was
	// told first. pkg.C sheds a site carry as the run reports it.
	prior := []Finding{attested("pkg.A", "a.go:1:1"), attested("pkg.B", "b.go:1:1")}
	store, _ := ledgerStore(t, prior)
	ledger := NewRunLedger(store, prior, "run-1", true)
	var delivered []string
	ledger.Shed = func(d AttestationShed) { delivered = append(delivered, d.Text()) }
	var committed []string
	ledger.Committed = func(f Finding, _ string) { committed = append(committed, f.Symbol) }
	ctx := context.Background()
	ledger.Contradiction(AttestationContradiction{Symbol: "pkg.B", Position: "b.go:1:1", Operator: "comparison: > -> >=", Killer: "TestB", Reason: "equivalent"})
	ledger.SiteShed(AttestationShed{Symbol: "pkg.C", Position: "c.go:1:1", Operator: "comparison: > -> >=", Reason: "site content changed"})
	ledger.SiteShed(AttestationShed{Symbol: "pkg.C", Position: "c.go:1:1", Operator: "comparison: > -> >=", Reason: "site content changed"})
	commit := ledger.Commit(ctx)
	killed := func(symbol string) Finding { return storeFinding(symbol, func(f *Finding) { f.Run = "run-1" }) }
	fresh := []Finding{killed("pkg.A"), killed("pkg.B"), killed("pkg.C")}
	for _, f := range fresh {
		if err := commit(f); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(committed, ",") != "pkg.A,pkg.B,pkg.C" {
		t.Fatalf("committed = %v", committed)
	}
	// A whole-tree finish runs the final merge over the run's targets,
	// so the residue below is the merge's own answer, not a short cut.
	outcome, err := ledger.Finish(ctx, fresh, []Target{{Symbol: "pkg.A"}, {Symbol: "pkg.B"}, {Symbol: "pkg.C"}}, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pkg.C c.go:1:1 comparison: > -> >= - site content changed",
		"pkg.A a.go:1:1 comparison: > -> >= - ",
	}
	if len(delivered) != 2 || delivered[0] != want[0] || !strings.HasPrefix(delivered[1], want[1]) {
		t.Fatalf("delivered = %q, want the site shed once and pkg.A's commit strip once, pkg.B's told by its contradiction", delivered)
	}
	if len(outcome.ResidueSheds) != 0 {
		t.Fatalf("residue = %+v, want nothing: every shed was reported at its commit", outcome.ResidueSheds)
	}
	for _, f := range outcome.Rendered {
		if len(f.Attested) != 0 {
			t.Fatalf("rendered %s still carries %+v", f.Symbol, f.Attested)
		}
	}
}

// The counts the epilogue states are the ledger's: a machine-local
// prior record whose fresh record is committable was promoted, a fresh
// record the store routes machine-local is counted, and a whole-tree
// reconcile's dropped symbols are owned (REQ-mcp-findings-doc,
// REQ-result-local-signpost, REQ-mcp-envelope).
func TestRunLedgerCountsPromotedMachineLocalAndDropped(t *testing.T) {
	prior := []Finding{
		storeFinding("pkg.Promoted", func(f *Finding) { f.Dirty = true }),
		storeFinding("pkg.Gone", nil),
		storeFinding("pkg.Kept", nil),
	}
	store, _ := ledgerStore(t, prior)
	ledger := NewRunLedger(store, prior, "run-2", true)
	ctx := context.Background()
	commit := ledger.Commit(ctx)
	fresh := []Finding{
		storeFinding("pkg.Promoted", func(f *Finding) { f.Run = "run-2" }),
		storeFinding("pkg.Local", func(f *Finding) { f.Run = "run-2"; f.Dirty = true }),
		storeFinding("pkg.Skipped", func(f *Finding) {
			// A skipped row carries no measurement.
			f.Run, f.Dirty, f.Skipped = "run-2", true, "no oracle"
			f.CandidateCount, f.Generated, f.Mutants, f.Killed, f.Operators = 0, 0, 0, 0, nil
		}),
	}
	for _, f := range fresh {
		if err := commit(f); err != nil {
			t.Fatal(err)
		}
	}
	// pkg.Skipped's standing record holds counts the skipped row must
	// not inherit.
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		return append(current, storeFinding("pkg.Skipped", nil)), nil
	}); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "pkg.Promoted"}, {Symbol: "pkg.Local"}, {Symbol: "pkg.Skipped"}, {Symbol: "pkg.Kept"}}
	outcome, err := ledger.Finish(ctx, fresh, targets, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Promoted != 1 || outcome.MachineLocal != 1 || outcome.Dropped != 1 {
		t.Fatalf("promoted %d, machine-local %d, dropped %d; want 1, 1, 1", outcome.Promoted, outcome.MachineLocal, outcome.Dropped)
	}
	// A skipped row is the run's own: the document's standing record
	// for its symbol never substitutes the zeroed counts, or the summary
	// would claim a prior measurement as this run's.
	for _, f := range outcome.Rendered {
		if f.Symbol == "pkg.Skipped" && (f.Killed != 0 || f.Generated != 0 || f.Mutants != 0) {
			t.Fatalf("skipped row took the document's counts: %+v", f)
		}
	}
	all, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for _, f := range all {
		symbols = append(symbols, f.Symbol)
	}
	if strings.Join(symbols, ",") == "" || strings.Contains(strings.Join(symbols, ","), "pkg.Gone") || !strings.Contains(strings.Join(symbols, ","), "pkg.Kept") {
		t.Fatalf("document after the reconcile = %v", symbols)
	}
	// A scoped selection of nothing writes nothing; a whole-tree one
	// reconciles, even against zero targets.
	scoped := NewRunLedger(store, all, "run-3", false)
	scoped.Update = func(context.Context, func([]Finding) ([]Finding, error)) error {
		t.Fatal("a scoped empty run wrote")
		return nil
	}
	if out, err := scoped.Finish(ctx, nil, nil, Selection{}); err != nil || out.Dropped != 0 {
		t.Fatalf("scoped empty finish = %+v, %v", out, err)
	}
	whole := NewRunLedger(store, all, "run-4", true)
	if out, err := whole.Finish(ctx, nil, nil, Selection{}); err != nil || out.Dropped != len(all) {
		t.Fatalf("whole-tree zero-target finish dropped %d of %d, %v", out.Dropped, len(all), err)
	}
}

// A commit that failed banks nothing: the committed hook fires only
// after the update returned, and the failure is the commit's answer
// (REQ-exec-cancellation's claims-only-committed clause).
func TestRunLedgerBanksOnlyReturnedCommits(t *testing.T) {
	store, _ := ledgerStore(t, nil)
	ledger := NewRunLedger(store, nil, "run-5", false)
	refused := errors.New("the document write refused")
	ledger.Update = func(context.Context, func([]Finding) ([]Finding, error)) error { return refused }
	ledger.Committed = func(f Finding, _ string) { t.Fatalf("a failed commit banked %s", f.Symbol) }
	if err := ledger.Commit(context.Background())(storeFinding("pkg.A", nil)); !errors.Is(err, refused) {
		t.Fatalf("commit error = %v, want the write's refusal", err)
	}
	// A final write that computed its reconcile and then failed to
	// persist it states no drop: the outcome reads the write's answer.
	gone := []Finding{storeFinding("pkg.Gone", nil)}
	whole := NewRunLedger(store, gone, "run-5", true)
	whole.Update = func(_ context.Context, change func([]Finding) ([]Finding, error)) error {
		if merged, err := change(gone); err != nil || len(merged) != 0 {
			t.Fatalf("the reconcile did not drop the stale record: %+v, %v", merged, err)
		}
		return refused
	}
	if outcome, err := whole.Finish(context.Background(), nil, nil, Selection{}); !errors.Is(err, refused) || outcome.Dropped != 0 {
		t.Fatalf("failed whole-tree write: dropped %d, %v; want 0 and the refusal", outcome.Dropped, err)
	}
}

// The final write re-judges every standing record against the
// exemptions in force, so a record this run never measured can leave
// the machine-local overlay on it: the promoted count owns that change
// too (REQ-mcp-findings-doc).
func TestRunLedgerCountsAStandingRecordTheWritePromotes(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, ".gomutant", "findings.json")
	unverifiable := storeFinding("pkg.Standing", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "sealed reason"
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeReason = "sealed reason"
	})
	ctx := context.Background()
	before, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{unverifiable}, nil }); err != nil {
		t.Fatal(err)
	}
	if layer, _ := before.Layer(unverifiable); layer != "local" {
		t.Fatalf("standing record layered %s before the exemption, want local", layer)
	}
	// The exemption lands between the runs; the next run's write moves
	// the record it never measured.
	record := `{"version":1,"exemptions":[{"subject":"pkg.Standing","reason":"sealed reason","rationale":"reviewed"},{"subject":"pkg.StandingTest","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(docPath), []byte(record), 0o644); err != nil {
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
	// The scoped run selected a target and merged nothing (every target
	// refused): the final write still happens, and it is the write that
	// moves the standing record.
	ledger := NewRunLedger(store, prior, "run-6", false)
	outcome, err := ledger.Finish(ctx, nil, []Target{{Symbol: "pkg.Measured"}}, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Promoted != 1 {
		t.Fatalf("promoted %d, want the standing record the write moved", outcome.Promoted)
	}
	if layer, _ := store.Layer(prior[0]); layer != "repo" {
		t.Fatalf("standing record layered %s after the write, want repo", layer)
	}
}

// The zero-target whole-tree write re-judges the standing records it
// keeps — the shaped ones — so a promotion can happen on it, and the
// outcome states it for the faces (REQ-mcp-findings-doc).
func TestRunLedgerCountsAPromotionOnTheZeroTargetWrite(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, ".gomutant", "findings.json")
	shaped := storeFinding("pkg.Shaped", func(f *Finding) {
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
		f.TargetEvidence = SubjectEvidence{}
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeReason = "sealed reason"
	})
	ctx := context.Background()
	before, err := OpenStore(docPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{shaped}, nil }); err != nil {
		t.Fatal(err)
	}
	if layer, _ := before.Layer(shaped); layer != "local" {
		t.Fatalf("shaped record layered %s before the exemption, want local", layer)
	}
	record := `{"version":1,"exemptions":[{"subject":"pkg.ShapedTest","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(docPath), []byte(record), 0o644); err != nil {
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
	ledger := NewRunLedger(store, prior, "run-7", true)
	outcome, err := ledger.Finish(ctx, nil, nil, Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Promoted != 1 || outcome.Dropped != 0 {
		t.Fatalf("zero-target write: promoted %d, dropped %d; want 1, 0", outcome.Promoted, outcome.Dropped)
	}
}

// The document changes a final merge persisted — a reconcile's drop, a
// promotion — ride an error exit after it on both faces: the counts
// fold into the error text, as sheds do on the structured face, and an
// error alone stands when nothing changed (REQ-mcp-findings-doc).
func TestRunOutcomePersistedChangesRideTheError(t *testing.T) {
	base := errors.New("render deadline")
	got := (RunOutcome{Dropped: 2, Promoted: 1}).PersistedRiding(base)
	if !errors.Is(got, base) || !strings.Contains(got.Error(), "render deadline — additionally, ") || !strings.Contains(got.Error(), "; 1 record(s) promoted - findings document changed, commit it (persisted)") {
		t.Fatalf("persisted changes riding the error = %v", got)
	}
	if got := (RunOutcome{Promoted: 1}).PersistedRiding(base); !strings.Contains(got.Error(), "additionally, 1 record(s) promoted") || strings.Contains(got.Error(), "; ") {
		t.Fatalf("a promotion alone riding the error = %v", got)
	}
	if got := (RunOutcome{}).PersistedRiding(base); got != base {
		t.Fatalf("no change wrapped the error: %v", got)
	}
	if got := (RunOutcome{Dropped: 2}).PersistedRiding(nil); got != nil {
		t.Fatalf("a drop minted an error out of success: %v", got)
	}
}

// The empty-selection note spells the changed input as the face's
// reader typed it — the wire knob on the structured face, the flag on
// the CLI — and never stutters after the CLI's "no targets:" lead.
func TestSelectionEmptiedNoteSpellsTheFacesInput(t *testing.T) {
	if got := SelectionEmptiedNote(false, "HEAD~1", "--changed"); got != "nothing changed vs HEAD~1; omit --changed to select the whole tree" {
		t.Fatalf("CLI note = %q", got)
	}
	if got := SelectionEmptiedNote(false, "HEAD~1", "changed"); got != "nothing changed vs HEAD~1; omit changed to select the whole tree" {
		t.Fatalf("wire note = %q", got)
	}
	if got := SelectionEmptiedNote(true, "HEAD~1", "changed"); !strings.HasPrefix(got, "the targets document selected zero") {
		t.Fatalf("document note = %q", got)
	}
}

// The reconcile's drop counts symbols, not records: a document
// hand-edited into duplicate records for one symbol cannot overcount
// what the reconcile dropped (REQ-mcp-findings-doc).
func TestDroppedSymbolsCountsEachSymbolOnce(t *testing.T) {
	rec := func(symbol string) Finding { return storeFinding(symbol, nil) }
	current := []Finding{rec("pkg.A"), rec("pkg.A"), rec("pkg.B"), rec("pkg.C")}
	if got := droppedSymbols(current, []Finding{rec("pkg.C")}); got != 2 {
		t.Fatalf("dropped = %d, want 2 (pkg.A once, pkg.B)", got)
	}
}
