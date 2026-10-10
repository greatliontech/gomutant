package gomutant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// An entry whose clause spells an in-module path by this checkout's
// absolute spelling — the module directory as given or its resolved
// form — is read as the module-relative clause Gofresh now produces,
// by the composers' grammar: the whole path after the composer's
// separator (a member's display/rel form, a moved bracket's root, the
// nested unverifiable form), the bracket-coverage parenthetical's own
// path, the root standing whole. An out-of-module path — one carrying
// the root's text inside it included — a sibling directory whose name
// extends the root's by any byte, and a path carrying ": " itself are
// left alone; two entries one re-key folds together are refused as
// the pair they are, the re-keyed spelling named (REQ-result-exemptions).
func TestExemptionRecordReKeysInModuleAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "exemptions.json")
	entry := func(subject, reason string) string {
		data, err := json.Marshal(Exemption{Subject: subject, Reason: reason, Rationale: "reviewed"})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	cases := []struct {
		name string
		from string
		to   string
	}{
		{"the path after the composer", "external directory input: " + dir + "/escape", "external directory input: escape"},
		{"the resolved spelling", "stat metadata input: " + resolved + "/data/a", "stat metadata input: data/a"},
		{"the bracket-coverage parenthetical, both paths", "runtime input not covered by observation bracket: " + dir + "/data/a (symlink outside every bracket root: " + dir + "/escape)", "runtime input not covered by observation bracket: data/a (symlink outside every bracket root: escape)"},
		{"a member's display/rel form", "unhashable runtime input: " + dir + "/lib/a.go", "unhashable runtime input: lib/a.go"},
		{"the root standing whole", "unhashable runtime input: " + dir, "unhashable runtime input: ."},
		{"the root closing the parenthetical", "runtime input not covered by observation bracket: data/a (symlink outside every bracket root: " + dir + ")", "runtime input not covered by observation bracket: data/a (symlink outside every bracket root: .)"},
		{"a moved bracket's root", "observation bracket moved: " + dir + "/fixtures", "observation bracket moved: fixtures"},
		{"the nested unverifiable form", "observation bracket unverifiable: runtime input not covered by observation bracket: " + dir + "/data/a (symlink outside every bracket root: " + dir + "/escape)", "observation bracket unverifiable: runtime input not covered by observation bracket: data/a (symlink outside every bracket root: escape)"},
		{"an out-of-module absolute path", "external directory input: /etc/hosts", "external directory input: /etc/hosts"},
		{"an out-of-module path carrying the root's text inside", "external runtime input target: /opt" + dir + "/config", "external runtime input target: /opt" + dir + "/config"},
		{"a sibling extending the root's name", "external directory input: " + dir + "2/x", "external directory input: " + dir + "2/x"},
		{"a sibling continuing with a space", "external directory input: " + dir + " old/data", "external directory input: " + dir + " old/data"},
		{"a sibling continuing with a comma", "external directory input: " + dir + ",v2/x", "external directory input: " + dir + ",v2/x"},
		{"a clause already module-relative", "external runtime input target: escape", "external runtime input target: escape"},
		{"a path carrying the separator itself (the fail-safe residual)", "external directory input: " + dir + "/a: b", "external directory input: " + dir + "/a: b"},
		{"a quoted member name (a control byte)", "unhashable runtime input: " + strconv.Quote(dir+"/lib/a\x01.go"), "unhashable runtime input: " + strconv.Quote("lib/a\x01.go")},
	}
	var rows []string
	for i, c := range cases {
		rows = append(rows, entry("p.T"+string(rune('A'+i)), c.from))
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[`+strings.Join(rows, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{dir, link} {
		got, rekeyed, err := LoadExemptions(path, root)
		if err != nil {
			t.Fatalf("under %s: %v", root, err)
		}
		var wantRekeyed []RekeyedExemption
		for i, c := range cases {
			if got[i].Reason != c.to {
				t.Fatalf("under %s, %s: clause %q, want %q", root, c.name, got[i].Reason, c.to)
			}
			if c.from != c.to {
				wantRekeyed = append(wantRekeyed, RekeyedExemption{Subject: "p.T" + string(rune('A'+i)), From: c.from, To: c.to})
			}
		}
		if len(rekeyed) != len(wantRekeyed) {
			t.Fatalf("under %s: re-keyed %+v, want %+v", root, rekeyed, wantRekeyed)
		}
		for i := range wantRekeyed {
			if rekeyed[i] != wantRekeyed[i] {
				t.Fatalf("under %s: re-keyed[%d] = %+v, want %+v", root, i, rekeyed[i], wantRekeyed[i])
			}
		}
	}
	// A record whose re-key collapses two entries onto one clause is
	// refused as the pair, naming both and the spelling the re-key
	// folded.
	pair := `{"version":1,"exemptions":[` + entry("p.TA", "external directory input: escape") + "," + entry("p.TA", "external directory input: "+dir+"/escape") + `]}`
	if err := os.WriteFile(path, []byte(pair), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadExemptions(path, dir); err == nil || !strings.Contains(err.Error(), "entries 0 and 1 both accept p.TA") || !strings.Contains(err.Error(), `(entry 1 re-keyed from "external directory input: `+dir+`/escape")`) {
		t.Fatalf("a pair one re-key folds together: %v; want the pair refusal naming the re-keyed spelling", err)
	}
	// The re-keyed clause matches a measurement spelled the new way.
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[`+entry("p.TA", "external directory input: "+dir+"/escape")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := LoadExemptions(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := exemptionFor(got, "p.TA", "external directory input: escape"); e == nil {
		t.Fatal("the re-keyed entry does not match the module-relative clause")
	}
	if e := exemptionFor(got, "p.TA", "external directory input: "+dir+"/escape"); e != nil {
		t.Fatal("the stale spelling still matches")
	}
}

// The re-keyed record is persisted by the store's first committing
// write — under the document lock, after the write's own work
// succeeded, once; a refused update and a store that only reads leave
// the file byte for byte and report nothing; a record that moved since
// the open (a revocation) is re-derived from the file as it stands,
// never overwritten with the copy loaded; a store opened after the
// write re-keys nothing; a revision persists exactly when it commits
// (REQ-result-exemptions).
func TestReKeyedExemptionsPersistAtTheFirstCommittingWrite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	stale := `{"version":1,"exemptions":[{"subject":"p.ATest","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reader, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The oracle's read taints the target's union evidence too (the
	// finding-wide anchor): both rows carry the clause, and the entry on
	// the oracle subject covers the finding.
	sealed := storeFinding("p.A", func(f *Finding) {
		for _, e := range []*SubjectEvidence{&f.TargetEvidence, &f.OracleEvidence[0]} {
			e.RuntimeUnverifiable = true
			e.RuntimeReason = "external directory input: escape"
		}
	})
	if layer, reason := reader.Layer(sealed); layer != LayerRepo {
		t.Fatalf("a read under the re-keyed entry layered %s (%s), want repo", layer, reason)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); string(got) != stale {
		t.Fatalf("a read-only store rewrote the record:\n%s", got)
	}
	if reader.RekeyedExemptions() != nil {
		t.Fatal("a store that wrote nothing reports a re-key")
	}
	// A refused update persists nothing.
	refused := errors.New("the update refuses")
	if _, err := reader.Update(ctx, func([]Finding) ([]Finding, error) { return nil, refused }); !errors.Is(err, refused) {
		t.Fatalf("the refusing update = %v", err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); string(got) != stale || reader.RekeyedExemptions() != nil {
		t.Fatalf("a refused update rewrote the record:\n%s", got)
	}
	writes := 0
	writer, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	writer.hooks.beforeExemptionWrite = func() error { writes++; return nil }
	for i := 0; i < 2; i++ {
		if _, err := writer.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatalf("the record was written %d times across two committing writes, want once", writes)
	}
	rekeyed := writer.RekeyedExemptions()
	if len(rekeyed) != 1 || rekeyed[0] != (RekeyedExemption{Subject: "p.ATest", From: "external directory input: " + dir + "/escape", To: "external directory input: escape"}) {
		t.Fatalf("re-keyed = %+v", rekeyed)
	}
	got, err := os.ReadFile(ExemptionsPathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"reason": "external directory input: escape"`) || strings.Contains(string(got), dir) {
		t.Fatalf("the persisted record still spells the path absolutely:\n%s", got)
	}
	// A record that moves after this store's persist (a reviewer adds
	// an unrelated entry mid-run): the refresh re-derives the pending
	// set, the report of what this store wrote stands.
	grown := `{"version":1,"exemptions":[{"subject":"p.ATest","reason":"external directory input: escape","rationale":"reviewed"},{"subject":"p.CTest","reason":"r","rationale":"why"}]}`
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	if again := writer.RekeyedExemptions(); len(again) != 1 || again[0] != rekeyed[0] {
		t.Fatalf("the report after a later move = %+v, want the one entry this store re-keyed", again)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); string(got) != grown {
		t.Fatalf("the moved record was written over:\n%s", got)
	}
	// A later move that brings a stale spelling in: the next write
	// re-keys it and the report names both writes' entries, in order.
	grownStale := `{"version":1,"exemptions":[{"subject":"p.ATest","reason":"external directory input: escape","rationale":"reviewed"},{"subject":"p.DTest","reason":"external directory input: ` + dir + `/late","rationale":"why"}]}`
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(grownStale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	if both := writer.RekeyedExemptions(); len(both) != 2 || both[0] != rekeyed[0] || both[1].Subject != "p.DTest" || both[1].To != "external directory input: late" {
		t.Fatalf("the report after a second re-keying write = %+v, want both writes' entries in order", both)
	}
	after, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := after.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	if after.RekeyedExemptions() != nil {
		t.Fatal("a store opened after the write re-keyed again")
	}
	// A record that moved since the open: the store loaded two stale
	// entries; a reviewer revokes the second before the first commit.
	// The write re-derives from the file as it stands — the revoked
	// entry never comes back, the remaining one is re-keyed.
	two := `{"version":1,"exemptions":[{"subject":"p.ATest","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"},{"subject":"p.BTest","reason":"external directory input: ` + dir + `/other","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	moved, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := moved.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(ExemptionsPathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "p.BTest") || strings.Contains(string(got), dir) || !strings.Contains(string(got), `"external directory input: escape"`) {
		t.Fatalf("the write undid the revocation or kept the stale spelling:\n%s", got)
	}
	if rekeyed := moved.RekeyedExemptions(); len(rekeyed) != 1 || rekeyed[0].Subject != "p.ATest" {
		t.Fatalf("re-keyed after the move = %+v, want the one entry the file still held", rekeyed)
	}
	// A record hand-fixed to the relative spelling before the first
	// commit: the re-derivation finds nothing stale — no write at all,
	// nothing reported.
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	fixed, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	relative := `{"version":1,"exemptions":[{"subject":"p.ATest","reason":"external directory input: escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(relative), 0o644); err != nil {
		t.Fatal(err)
	}
	fixedWrites := 0
	fixed.hooks.beforeExemptionWrite = func() error { fixedWrites++; return nil }
	if _, err := fixed.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); fixedWrites != 0 || string(got) != relative || fixed.RekeyedExemptions() != nil {
		t.Fatalf("a hand-fixed record was rewritten (%d writes) or reported:\n%s", fixedWrites, got)
	}
	// A record torn since the open carries no decidable entries: the
	// write proceeds under the entries in force (a campaign commits
	// through its prepared store to the end) and nothing is written
	// over the torn file; a failing record write refuses the write
	// before the document's own — never a landed document reported as
	// a failed commit.
	tornDoc := filepath.Join(t.TempDir(), "findings.json")
	tornStale := strings.ReplaceAll(stale, dir+"/escape", filepath.Dir(tornDoc)+"/escape")
	if err := os.WriteFile(ExemptionsPathFor(tornDoc), []byte(tornStale), 0o644); err != nil {
		t.Fatal(err)
	}
	tornStore, err := OpenStore(tornDoc, filepath.Dir(tornDoc))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExemptionsPathFor(tornDoc), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if routing, err := tornStore.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil || routing["p.A"].Layer != LayerRepo {
		t.Fatalf("the write over a torn record = %+v, %v; want it to proceed under the entries in force", routing, err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(tornDoc)); string(got) != "{" || tornStore.RekeyedExemptions() != nil {
		t.Fatalf("the torn record was written over or a re-key reported:\n%s", got)
	}
	if _, err := os.Stat(tornDoc); err != nil {
		t.Fatalf("the document was not written under the torn record: %v", err)
	}
	// The torn record restored to the bytes loaded: the next write
	// persists the pending re-key after all.
	if err := os.WriteFile(ExemptionsPathFor(tornDoc), []byte(tornStale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tornStore.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(tornDoc)); strings.Contains(string(got), filepath.Dir(tornDoc)) || len(tornStore.RekeyedExemptions()) != 1 {
		t.Fatalf("the restored record was not re-keyed at the next write:\n%s", got)
	}
	// A measuring write failing AFTER the re-key persisted (its overlay
	// edit refused by a read-only overlay) names the persisted re-key.
	if os.Getuid() == 0 {
		t.Skip("a read-only directory does not refuse root")
	}
	lateDir := t.TempDir()
	lateDoc := filepath.Join(lateDir, "findings.json")
	if err := os.WriteFile(ExemptionsPathFor(lateDoc), []byte(strings.ReplaceAll(stale, dir+"/escape", lateDir+"/escape")), 0o644); err != nil {
		t.Fatal(err)
	}
	lateStore, err := OpenStore(lateDoc, lateDir)
	if err != nil {
		t.Fatal(err)
	}
	// The overlay directory appears at the first overlay write; here
	// it exists read-only ahead of it.
	if err := os.MkdirAll(lateStore.overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lateStore.overlayDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lateStore.overlayDir, 0o755) })
	local := sealed
	local.Dirty = true
	_, err = lateStore.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{local}, nil })
	if err == nil || !strings.Contains(err.Error(), "the exemption record was rewritten (the re-key persisted) ahead of the write that failed: ") {
		t.Fatalf("a measuring write failing after the persist = %v; want the persisted re-key named", err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(lateDoc)); strings.Contains(string(got), lateDir) || len(lateStore.RekeyedExemptions()) != 1 {
		t.Fatalf("the re-key did not persist ahead of the failed write:\n%s", got)
	}
	failDoc := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(ExemptionsPathFor(failDoc), []byte(strings.ReplaceAll(stale, dir+"/escape", filepath.Dir(failDoc)+"/escape")), 0o644); err != nil {
		t.Fatal(err)
	}
	failStore, err := OpenStore(failDoc, filepath.Dir(failDoc))
	if err != nil {
		t.Fatal(err)
	}
	failStore.hooks.beforeExemptionWrite = func() error { return errors.New("the record write fails") }
	if _, err := failStore.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{sealed}, nil }); err == nil {
		t.Fatal("the write was not refused under a failing record write")
	}
	if _, err := os.Stat(failDoc); !os.IsNotExist(err) {
		t.Fatalf("the document was written under a refused record write (%v)", err)
	}
	if failStore.RekeyedExemptions() != nil {
		t.Fatal("a refused write reported a re-key")
	}
	// A record judged committable under an entry revoked since the
	// open is judged afresh at the write: the memo does not carry the
	// old answer.
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	memo, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	other := storeFinding("p.B", func(f *Finding) {
		for _, e := range []*SubjectEvidence{&f.TargetEvidence, &f.OracleEvidence[0]} {
			e.RuntimeUnverifiable = true
			e.RuntimeReason = "external directory input: other"
		}
	})
	// The first write judges it through the routing memo (a Layer read
	// never fills the memo); the second, under the revocation, judges
	// it afresh with the same persisted form.
	first, err := memo.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{other}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if first["p.B"].Layer != LayerRepo {
		t.Fatalf("under the second entry the record routed %+v, want repo", first["p.B"])
	}
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	routing, err := memo.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{other}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if routing["p.B"].Layer != LayerLocal {
		t.Fatalf("the record judged under a revoked entry routed %+v, want machine-local", routing["p.B"])
	}
	// A revision under check persists nothing; a committing revision
	// persists the re-key as a measuring write does.
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	checked, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	keep := func(layer string, f Finding) (Finding, bool, error) { return f, true, nil }
	if _, err := checked.Revise(ctx, true, keep); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); string(got) != stale || checked.RekeyedExemptions() != nil {
		t.Fatalf("a revision under check rewrote the record:\n%s", got)
	}
	if _, err := checked.Revise(ctx, false, keep); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(path)); strings.Contains(string(got), dir) || len(checked.RekeyedExemptions()) != 1 {
		t.Fatalf("a committing revision left the record stale:\n%s", got)
	}
	if line := RekeyedExemptionsLine(checked.RekeyedExemptions()); !strings.HasPrefix(line, `re-keyed 1 reviewed exemption clause(s) to the canonical spelling: p.ATest "external directory input: `+dir+`/escape" -> "external directory input: escape"`) {
		t.Fatalf("line = %q", line)
	}
	if RekeyedExemptionsLine(nil) != "" {
		t.Fatal("an empty re-key renders a line")
	}
	// An error exit after the write carries the line: the run's
	// post-write refusals fold it with the other persisted changes.
	riding := RunOutcome{ExemptionsRekeyed: checked.RekeyedExemptions()}.PersistedRiding(errors.New("drift"))
	if riding == nil || !strings.Contains(riding.Error(), "additionally, re-keyed 1 reviewed exemption clause(s)") || !strings.Contains(riding.Error(), "(persisted)") {
		t.Fatalf("the error exit = %v; want the re-key riding it", riding)
	}
	many := make([]RekeyedExemption, rekeyedExemptionRoster+3)
	for i := range many {
		many[i] = RekeyedExemption{Subject: "p.T", From: "a", To: "b"}
	}
	if line := RekeyedExemptionsLine(many); !strings.HasSuffix(line, " (+3 more)") || !strings.HasPrefix(line, "re-keyed 23 ") {
		t.Fatalf("the roster is unbounded or uncounted: %q", line)
	}
}

// The record verbs persist the re-key exactly when they write: a
// preview leaves the record as it stands and reports nothing; a
// committing retarget persists it with its own subject rewrite (the
// moved subject, the module-relative clause), a committing prune
// persists it, each listed on its result (REQ-result-exemptions,
// REQ-result-lifecycle).
func TestRecordVerbsPersistTheReKeyWhenTheyWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree, store := lifecycleModule(t, old)
	ctx := context.Background()
	stale := `{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"external directory input: ` + store.moduleDir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", true)
	if err != nil || preview.ExemptionsRekeyed != nil {
		t.Fatalf("the preview = %+v, %v; want no re-key reported", preview.ExemptionsRekeyed, err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(store.path)); string(got) != stale {
		t.Fatalf("the preview rewrote the record:\n%s", got)
	}
	result, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", false)
	if err != nil || len(result.ExemptionsRekeyed) != 1 || result.ExemptionsRekeyed[0].Subject != "example.com/old.TestF" {
		t.Fatalf("the retarget = %+v, %v; want the re-keyed entry listed", result.ExemptionsRekeyed, err)
	}
	got, err := os.ReadFile(ExemptionsPathFor(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"subject": "example.com/life.TestF"`) || !strings.Contains(string(got), `"reason": "external directory input: escape"`) || strings.Contains(string(got), store.moduleDir) {
		t.Fatalf("the committing retarget left the record stale or unmoved:\n%s", got)
	}
	// A retarget whose moved entry was revoked since the open writes
	// the entries in force: the revocation holds, the stale one is
	// re-keyed, the listing names what was rewritten.
	twoEntries := `{"version":1,"exemptions":[{"subject":"example.com/life.TestF","reason":"external directory input: ` + store.moduleDir + `/escape","rationale":"reviewed"},{"subject":"example.com/old.TestUnmeasured","reason":"r","rationale":"why"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(twoEntries), 0o644); err != nil {
		t.Fatal(err)
	}
	revoking, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	oneEntry := `{"version":1,"exemptions":[{"subject":"example.com/life.TestF","reason":"external directory input: ` + store.moduleDir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(oneEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	moved, err := tree.RetargetContext(ctx, revoking, "example.com/old.", "example.com/life.", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Exemptions) != 0 || len(moved.ExemptionsRekeyed) != 1 {
		t.Fatalf("the retarget over the revoked record = subjects %+v, re-keyed %+v; want the revocation held and the stale entry re-keyed", moved.Exemptions, moved.ExemptionsRekeyed)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(store.path)); strings.Contains(string(got), "TestUnmeasured") || strings.Contains(string(got), store.moduleDir) {
		t.Fatalf("the retarget wrote the revoked entry back or left the stale one:\n%s", got)
	}
	// Prune, over a fresh stale record.
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	pruner, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	if pre, err := tree.PruneDetachedContext(ctx, pruner, true); err != nil || pre.ExemptionsRekeyed != nil {
		t.Fatalf("the prune preview = %+v, %v; want no re-key reported", pre.ExemptionsRekeyed, err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(store.path)); string(got) != stale {
		t.Fatalf("the prune preview rewrote the record:\n%s", got)
	}
	pruned, err := tree.PruneDetachedContext(ctx, pruner, false)
	if err != nil || len(pruned.ExemptionsRekeyed) != 1 {
		t.Fatalf("the prune = %+v, %v; want the re-keyed entry listed", pruned.ExemptionsRekeyed, err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(store.path)); strings.Contains(string(got), store.moduleDir) {
		t.Fatalf("the committing prune left the record stale:\n%s", got)
	}
}
