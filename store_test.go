package gomutant

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// storeManifest builds a canonical runtimeinput manifest (the wire form
// gofresh's runtimeinput package decodes) over absolute path inputs.
func storeManifest(paths ...string) string {
	doc := `{"v":1}`
	if len(paths) > 0 {
		entries := make([]string, len(paths))
		for i, p := range paths {
			entries[i] = fmt.Sprintf(`{"k":"abs","p":%q,"d":"0123456789abcdef0123456789abcdef"}`, p)
		}
		doc = `{"v":1,"paths":[` + strings.Join(entries, ",") + `]}`
	}
	return base64.RawURLEncoding.EncodeToString([]byte(doc))
}

func cleanEvidence(symbol string) SubjectEvidence {
	return SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
		ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
		ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
		RuntimeInputs: storeManifest(), RuntimeDigest: "digest"}
}

func storeFinding(symbol string, mutate func(*Finding)) Finding {
	f := Finding{Symbol: symbol, BodyHash: "h", OperatorSet: "go/2", OracleTimeout: "1m0s",
		Commit: "abc", TargetEvidence: cleanEvidence(symbol),
		OracleEvidence: []SubjectEvidence{cleanEvidence(symbol + "Test")},
		CandidateCount: 1, Generated: 1, Mutants: 1, Killed: 1,
		Operators: []OperatorSummary{{Operator: "zero return", Generated: 1, Killed: 1}}}
	if mutate != nil {
		mutate(&f)
	}
	return f
}

// Committability draws the portable line: clean commit-pinned evidence
// is repo material; dirty provenance, missing commits,
// runtime-unverifiable evidence, and machine-local input identities
// stay local (REQ-result-layers).
func TestCommittableDrawsThePortableLine(t *testing.T) {
	dir := t.TempDir()
	if ok, reason := Committable(storeFinding("p.A", nil), dir, nil); !ok {
		t.Fatalf("clean finding not committable: %s", reason)
	}
	cases := []struct {
		name   string
		mutate func(*Finding)
		want   string
	}{
		{"dirty", func(f *Finding) { f.Dirty = true }, "dirty worktree"},
		{"no commit", func(f *Finding) { f.Commit = "" }, "no commit"},
		{"unverifiable target", func(f *Finding) { f.TargetEvidence.RuntimeUnverifiable = true }, "runtime-unverifiable"},
		{"unverifiable oracle", func(f *Finding) { f.OracleEvidence[0].RuntimeUnverifiable = true }, "runtime-unverifiable"},
		{"machine-local input", func(f *Finding) { f.TargetEvidence.RuntimeInputs = storeManifest("/etc/hosts") }, "machine-local runtime input /etc/hosts"},
	}
	for _, tc := range cases {
		if ok, reason := Committable(storeFinding("p.A", tc.mutate), dir, nil); ok || !strings.Contains(reason, tc.want) {
			t.Fatalf("%s: committable=%v reason=%q, want reason containing %q", tc.name, ok, reason, tc.want)
		}
	}
}

// The full portable-line walk names every failing clause, deduplicated,
// with Committable's single reason as its first element - repairing one
// clause never surfaces the next as a surprise (REQ-result-layers via
// the explain surface).
func TestCommittableReasonsListEveryFailingClause(t *testing.T) {
	dir := t.TempDir()
	if reasons := CommittableReasons(storeFinding("p.A", nil), dir, nil); len(reasons) != 0 {
		t.Fatalf("clean finding carries reasons: %v", reasons)
	}
	shared := storeManifest("/etc/hosts")
	f := storeFinding("p.A", func(f *Finding) {
		f.Dirty = true
		f.Commit = ""
		f.TargetEvidence.RuntimeInputs = shared
		f.OracleEvidence[0].RuntimeUnverifiable = true
		// Parsed records always pair the flag with the recorded reason
		// (the import validator refuses the mismatch), and the clause
		// serves the reason — it carries the discharge channel, so the
		// layer disqualifier must not be the one unverifiable answer
		// that dead-ends.
		f.OracleEvidence[0].RuntimeReason = "shared dynamic state (dischargeable by a caller vouch for x:y)"
		f.OracleEvidence[0].RuntimeInputs = shared
	})
	want := []string{
		"dirty worktree provenance",
		"no commit provenance",
		"machine-local runtime input /etc/hosts",
		"runtime-unverifiable evidence for p.ATest: shared dynamic state (dischargeable by a caller vouch for x:y)",
	}
	reasons := CommittableReasons(f, dir, nil)
	if len(reasons) != len(want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
	for i := range want {
		if reasons[i] != want[i] {
			t.Fatalf("reasons[%d] = %q, want %q (full: %v)", i, reasons[i], want[i], reasons)
		}
	}
	if ok, first := Committable(f, dir, nil); ok || first != reasons[0] {
		t.Fatalf("Committable = %v %q, want the walk's first clause %q", ok, first, reasons[0])
	}
	// An unreadable manifest on one subject never truncates the walk:
	// the next subject's clauses still surface.
	torn := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.RuntimeInputs = "!!"
		f.OracleEvidence[0].RuntimeInputs = storeManifest("/etc/hosts")
	})
	want = []string{
		"unreadable runtime manifest for p.A",
		"machine-local runtime input /etc/hosts",
	}
	reasons = CommittableReasons(torn, dir, nil)
	if len(reasons) != len(want) || reasons[0] != want[0] || reasons[1] != want[1] {
		t.Fatalf("torn-manifest walk = %v, want %v", reasons, want)
	}
}

// The survivor-advice vocabulary is the explain surface's contract: one
// prescription per execution bucket, advisory, never a verdict
// (REQ-result-findings).
func TestSurvivorAdviceVocabulary(t *testing.T) {
	want := map[string]string{
		"never-executed":      "no oracle test executes the mutated position - extend a test to reach it",
		"executed-and-passed": "the position executes and every oracle assertion still passes - sharpen an assertion or attest an equivalence",
		"overlay-bypassed":    "the oracle's observed reads include a mutated file's own on-disk path - its verdict came from the unmutated tree, not the built mutant; restructure the test to judge the linked build (a pure core over in-memory inputs) instead of re-reading the tree",
		"unstable-oracle":     "the finding's runtime evidence is unverifiable - stabilize the oracle's runtime inputs before trusting execution evidence",
		"":                    "execution evidence unavailable - the coverage probe was refused or the record predates bucketing; re-measure to bucket this survivor",
	}
	for bucket, advice := range want {
		if got := SurvivorAdvice(bucket); got != advice {
			t.Fatalf("SurvivorAdvice(%q) = %q, want %q", bucket, got, advice)
		}
	}
}

// The write splits by committability, the read merges with the overlay
// winning, a committable successor evicts its overlay entry, a local
// successor never evicts portable repo truth, and a pruned symbol
// leaves both layers (REQ-result-layers).
func TestStoreSplitsUpdatesAcrossLayers(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	clean := storeFinding("p.A", nil)
	local := storeFinding("p.B", func(f *Finding) { f.Dirty = true })
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{clean, local}, nil }); err != nil {
		t.Fatal(err)
	}
	repoData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := ParseFindings(repoData)
	if err != nil || len(repo) != 1 || repo[0].Symbol != "p.A" {
		t.Fatalf("repo layer = %+v, %v; want the committable record alone", repo, err)
	}
	merged, err := store.Load(ctx)
	if err != nil || len(merged) != 2 {
		t.Fatalf("merged view = %+v, %v", merged, err)
	}

	// A local successor for the clean symbol shadows the merged view but
	// never evicts the portable repo row.
	dirtyA := storeFinding("p.A", func(f *Finding) { f.Dirty = true; f.BodyHash = "h2" })
	if err := store.Update(ctx, func(prior []Finding) ([]Finding, error) {
		next := append([]Finding(nil), prior...)
		for i := range next {
			if next[i].Symbol == "p.A" {
				next[i] = dirtyA
			}
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	repoData, _ = os.ReadFile(path)
	repo, _ = ParseFindings(repoData)
	if len(repo) != 1 || repo[0].Symbol != "p.A" || repo[0].BodyHash != "h" {
		t.Fatalf("repo layer after local successor = %+v; want the portable row preserved", repo)
	}
	merged, _ = store.Load(ctx)
	var gotA Finding
	for _, f := range merged {
		if f.Symbol == "p.A" {
			gotA = f
		}
	}
	if gotA.BodyHash != "h2" {
		t.Fatalf("merged view a = %+v; want the overlay winning", gotA)
	}

	// A committable successor for the local symbol evicts its overlay
	// entry and lands in the repo document.
	cleanB := storeFinding("p.B", nil)
	if err := store.Update(ctx, func(prior []Finding) ([]Finding, error) {
		next := append([]Finding(nil), prior...)
		for i := range next {
			if next[i].Symbol == "p.B" {
				next[i] = cleanB
			}
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	repoData, _ = os.ReadFile(path)
	repo, _ = ParseFindings(repoData)
	if len(repo) != 2 {
		t.Fatalf("repo layer after committable successor = %+v", repo)
	}
	if _, err := os.Stat(store.entryPath("p.B")); !os.IsNotExist(err) {
		t.Fatalf("overlay entry for the committable successor survived: %v", err)
	}

	// Pruning a symbol clears both layers.
	if err := store.Update(ctx, func(prior []Finding) ([]Finding, error) {
		var next []Finding
		for _, f := range prior {
			if f.Symbol != "p.A" {
				next = append(next, f)
			}
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	merged, _ = store.Load(ctx)
	for _, f := range merged {
		if f.Symbol == "p.A" {
			t.Fatalf("pruned symbol resurrected: %+v", merged)
		}
	}
	if _, err := os.Stat(store.entryPath("p.A")); !os.IsNotExist(err) {
		t.Fatal("pruned symbol's overlay entry survived")
	}

	repoCount, localOnly, err := store.Committability(ctx)
	if err != nil || repoCount != 1 || localOnly != 0 {
		t.Fatalf("committability = %d/%d, %v", repoCount, localOnly, err)
	}
}

// survivorFinding is a machine-local (dirty) finding carrying one open
// survivor, satisfying the candidate-conservation equations.
func survivorFinding(symbol string) Finding {
	return storeFinding(symbol, func(f *Finding) {
		f.Dirty = true
		f.Killed = 0
		f.Labels = []string{"requirement"}
		f.Survivors = []Survivor{{Position: "a.go:1:1", Operator: "zero return"}}
		f.Operators = []OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}}
		f.Attested = []Attestation{{Position: "a.go:1:1", Operator: "zero return", Reason: "equivalent"}}
	})
}

// paddedEntryDoc builds a valid single-finding overlay document padded
// with an unknown field (dropped per REQ-result-tolerant) to exactly
// size bytes.
func paddedEntryDoc(t *testing.T, symbol string, size int) []byte {
	t.Helper()
	doc, err := Export([]Finding{survivorFinding(symbol)})
	if err != nil {
		t.Fatal(err)
	}
	pad := size - (len(doc) - 1) - len(`,"pad":"`) - len(`"}`)
	if pad < 0 {
		t.Fatalf("padding target %d smaller than the base document", size)
	}
	return []byte(string(doc[:len(doc)-1]) + `,"pad":"` + strings.Repeat("A", pad) + `"}`)
}

// writePaddedEntry writes a padded valid entry at the symbol's overlay
// path.
func writePaddedEntry(t *testing.T, store *Store, symbol string, size int) {
	t.Helper()
	if err := os.MkdirAll(store.overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.entryPath(symbol), paddedEntryDoc(t, symbol, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadSymbols maps the merged view by symbol.
func loadSymbols(t *testing.T, store *Store) map[string]Finding {
	t.Helper()
	merged, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]Finding, len(merged))
	for _, f := range merged {
		out[f.Symbol] = f
	}
	return out
}

// An overlay entry over the evidence ceiling is discarded like a
// malformed one — well-formed residue must not tax every later read —
// while an entry exactly at the ceiling remains served evidence
// (REQ-result-layers).
func TestOverlayEvictsEntriesOverTheEvidenceCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	writePaddedEntry(t, store, "p.Over", overlayEntryCeiling+1)
	writePaddedEntry(t, store, "p.At", overlayEntryCeiling)

	got := loadSymbols(t, store)
	if _, ok := got["p.Over"]; ok {
		t.Fatal("an over-ceiling overlay entry was served")
	}
	if _, ok := got["p.At"]; !ok {
		t.Fatal("an at-ceiling overlay entry was evicted")
	}
	if _, err := os.Stat(store.entryPath("p.Over")); !os.IsNotExist(err) {
		t.Fatalf("the over-ceiling entry survived on disk: %v", err)
	}
	if _, err := os.Stat(store.entryPath("p.At")); err != nil {
		t.Fatalf("the at-ceiling entry left disk: %v", err)
	}
}

// The ceiling judges the content a read would consume: a small symlink
// at an over-ceiling target is evicted unread, never parsed and served
// (REQ-result-layers).
func TestOverlayCeilingFollowsSymlinkedEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, paddedEntryDoc(t, "p.Linked", overlayEntryCeiling+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.entryPath("p.Linked")); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSymbols(t, store)["p.Linked"]; ok {
		t.Fatal("an over-ceiling symlinked entry was parsed and served")
	}
	if _, err := os.Lstat(store.entryPath("p.Linked")); !os.IsNotExist(err) {
		t.Fatalf("the over-ceiling symlinked entry survived in the overlay: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("eviction reached through the symlink to its target: %v", err)
	}
}

// An overlay entry whose file identity is unchanged is served without a
// re-read: same-size corruption behind an unchanged stat still serves
// the prior parse (the tolerated stale-winner shape), and a moved stat
// re-reads and judges the current bytes (REQ-result-layers).
func TestOverlayServesUnchangedEntriesWithoutReparsing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) {
		return []Finding{survivorFinding("p.A")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSymbols(t, store)["p.A"]; !ok {
		t.Fatal("installed overlay entry not served")
	}

	entry := store.entryPath("p.A")
	info, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	garbage := strings.Repeat("X", int(info.Size()))
	if err := os.WriteFile(entry, []byte(garbage), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(entry, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got, ok := loadSymbols(t, store)["p.A"]; !ok || got.BodyHash != "h" {
		t.Fatalf("unchanged-stat entry not served from the prior parse: %+v ok=%v", got, ok)
	}
	if data, err := os.ReadFile(entry); err != nil || string(data) != garbage {
		t.Fatalf("unchanged-stat entry was re-read and judged: %v", err)
	}

	if err := os.Chtimes(entry, info.ModTime().Add(2*time.Second), info.ModTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSymbols(t, store)["p.A"]; ok {
		t.Fatal("a moved stat served the stale parse instead of judging the current bytes")
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("the malformed re-read entry survived on disk: %v", err)
	}
}

// An install primes the parse cache with exactly the bytes it wrote, so
// a run's own incremental commits never re-parse their own installs
// (REQ-result-layers).
func TestOverlayInstallWarmsTheParseCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
		served := survivorFinding("p.A")
		served.Cached = true
		return []Finding{served}, nil
	}); err != nil {
		t.Fatal(err)
	}
	entry := store.entryPath("p.A")
	info, err := os.Stat(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(strings.Repeat("X", int(info.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(entry, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	got, ok := loadSymbols(t, store)["p.A"]
	if !ok || got.BodyHash != "h" {
		t.Fatalf("first read after install re-parsed instead of serving the install's parse: %+v ok=%v", got, ok)
	}
	// The warm entry mirrors a parse of the written bytes: run metadata
	// like the served-from-cache marker never survives persistence.
	if got.Cached {
		t.Fatal("never-persisted run metadata served from the install-warmed parse")
	}
}

// The directory listing stays the membership authority over the parse
// cache: a rewritten entry serves its current bytes and a deleted entry
// leaves the merged view (REQ-result-layers).
func TestOverlayReloadTracksRewrittenAndDeletedEntries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
		return []Finding{survivorFinding("p.A")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSymbols(t, store)["p.A"]; !ok {
		t.Fatal("installed overlay entry not served")
	}

	rewritten := survivorFinding("p.A")
	rewritten.BodyHash = "h2"
	doc, err := Export([]Finding{rewritten})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.entryPath("p.A"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadSymbols(t, store)["p.A"]; got.BodyHash != "h2" {
		t.Fatalf("rewritten entry served a stale parse: %+v", got)
	}

	if err := os.Remove(store.entryPath("p.A")); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSymbols(t, store)["p.A"]; ok {
		t.Fatal("deleted entry served from the parse cache")
	}
}

// A caller's in-place edit of a merged view never alters what a later
// read serves: an aborted update's mutations — a rewritten survivor, an
// attestation appended into a shared backing array — must not surface
// as persisted evidence (REQ-result-layers, REQ-attest-survivor).
func TestOverlayMergedViewIsIsolatedFromCallerMutation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) {
		return []Finding{survivorFinding("p.A")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	abort := fmt.Errorf("abort after mutating the merged view")
	if err := store.Update(ctx, func(all []Finding) ([]Finding, error) {
		for i := range all {
			if all[i].Symbol == "p.A" {
				all[i].Labels[0] = "corrupted"
				all[i].OracleEvidence[0].Symbol = "corrupted"
				all[i].Operators[0].Operator = "corrupted"
				all[i].Survivors[0].Operator = "corrupted"
				all[i].Attested[0].Reason = "corrupted"
			}
		}
		return nil, abort
	}); err != abort {
		t.Fatalf("aborted update returned %v", err)
	}
	got, ok := loadSymbols(t, store)["p.A"]
	if !ok {
		t.Fatal("finding lost after aborted update")
	}
	intact := got.Labels[0] == "requirement" && got.OracleEvidence[0].Symbol == "p.ATest" &&
		got.Operators[0].Operator == "zero return" && got.Survivors[0].Operator == "zero return" &&
		got.Attested[0].Reason == "equivalent"
	if !intact {
		t.Fatalf("aborted update's mutations surfaced in a later read: %+v", got)
	}
}

// The caller's update runs while the repo document's lock is held, so a
// concurrent session cannot commit between the read and the split and
// have its rows silently pruned by stale membership — the second writer
// waits on the lock instead.
func TestStoreUpdateDecidesMembershipUnderTheDocumentLock(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(prior []Finding) ([]Finding, error) {
		nested, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := second.Update(nested, func(p []Finding) ([]Finding, error) { return p, nil }); err == nil {
			t.Fatal("a second session's update proceeded while the caller's update held the document lock")
		}
		return prior, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Machine-local input clauses sharing a top-level directory roll up
// into one row naming the root and count; singletons and non-input
// clauses pass through in place (REQ-mcp-explain).
func TestRollUpMachineLocalInputs(t *testing.T) {
	in := []string{
		"dirty worktree provenance",
		"machine-local runtime input /tmp/layer-oracle-1/a",
		"machine-local runtime input /tmp/layer-oracle-2/b",
		"machine-local runtime input /tmp/layer-oracle-3/c",
		"runtime-unverifiable evidence for p.A",
		"machine-local runtime input /etc/hostname",
	}
	got := RollUpMachineLocalInputs(in)
	want := []string{
		"dirty worktree provenance",
		"machine-local runtime inputs under /tmp (3 paths)",
		"runtime-unverifiable evidence for p.A",
		"machine-local runtime input /etc/hostname",
	}
	if len(got) != len(want) {
		t.Fatalf("rolled = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rolled = %q, want %q", got, want)
		}
	}
}

// A version-ahead overlay entry is a newer binary's record, not
// corruption: a stale long-lived reader refuses the read with the
// restart signal instead of silently destroying machine-local evidence
// (REQ-result-export's version-ahead arm on the overlay layer).
func TestOverlayVersionAheadRefusesInsteadOfDeleting(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	path := store.entryPath("p.Ahead")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version": 99, "findings": [{}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "newer gomutant likely wrote it") {
		t.Fatalf("version-ahead overlay read = %v, want the restart-signal refusal", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("version-ahead overlay entry destroyed: %v", statErr)
	}
	// Genuine garbage is still swept.
	garbage := store.entryPath("p.Garbage")
	if err := os.WriteFile(garbage, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); err != nil {
		t.Fatalf("garbage entry failed the read: %v", err)
	}
	if _, statErr := os.Stat(garbage); !os.IsNotExist(statErr) {
		t.Fatal("garbage overlay entry survived the sweep")
	}
}

// A commit rewrites only the entries whose record it changed and
// judges only the records it changed: the other entries keep their
// stat identity and pay no portable-line walk (REQ-result-layers).
func TestStoreUpdateRewritesOnlyChangedEntries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	local := func(symbol string) Finding { return storeFinding(symbol, func(f *Finding) { f.Dirty = true }) }
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{local("p.A"), local("p.B"), local("p.C")}, nil }); err != nil {
		t.Fatal(err)
	}
	identity := func(symbol string) string {
		info, err := os.Stat(store.entryPath(symbol))
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano())
	}
	before := map[string]string{"p.A": identity("p.A"), "p.B": identity("p.B"), "p.C": identity("p.C")}
	var walked []string
	store.hooks.walk = func(symbol string) { walked = append(walked, symbol) }
	changedB := local("p.B")
	changedB.BodyHash = "h2"
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		next := append([]Finding(nil), current...)
		for i := range next {
			if next[i].Symbol == "p.B" {
				next[i] = changedB
			}
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	if identity("p.A") != before["p.A"] || identity("p.C") != before["p.C"] {
		t.Fatalf("unchanged entries rewritten: A %s→%s, C %s→%s", before["p.A"], identity("p.A"), before["p.C"], identity("p.C"))
	}
	if identity("p.B") == before["p.B"] {
		t.Fatal("the changed entry was not rewritten")
	}
	if len(walked) != 1 || walked[0] != "p.B" {
		t.Fatalf("portable-line walks = %v; want the changed record alone", walked)
	}
	merged, err := store.Load(ctx)
	if err != nil || len(merged) != 3 {
		t.Fatalf("merged = %+v, %v", merged, err)
	}
	for _, f := range merged {
		if f.Symbol == "p.B" && f.BodyHash != "h2" {
			t.Fatalf("changed record not served: %+v", f)
		}
	}
}

// The change comparison reads the record's persisted form: a served
// record (Cached, never persisted) that is otherwise unchanged rewrites
// nothing; and an entry deleted from under an unchanged record is
// reinstalled, the overlay's own state deciding the write.
func TestStoreUpdateComparesThePersistedForm(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	local := storeFinding("p.A", func(f *Finding) { f.Dirty = true })
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{local}, nil }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.entryPath("p.A"))
	if err != nil {
		t.Fatal(err)
	}
	served := local
	served.Cached = true
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{served}, nil }); err != nil {
		t.Fatal(err)
	}
	if again, err := os.Stat(store.entryPath("p.A")); err != nil || !again.ModTime().Equal(info.ModTime()) || again.Size() != info.Size() {
		t.Fatalf("a served, otherwise unchanged record rewrote its entry: %v", err)
	}
	if err := os.Remove(store.entryPath("p.A")); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{local}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.entryPath("p.A")); err != nil {
		t.Fatalf("an unchanged record whose entry was deleted was not reinstalled: %v", err)
	}
}

// A record that becomes committable without changing content — an
// exemption now covers its unverifiable evidence — loses its overlay
// entry the moment it commits, so the repo row is never shadowed.
func TestStoreDeletesTheEntryWhenAnUnchangedRecordBecomesCommittable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	unverifiable := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "clock"
		for i := range f.OracleEvidence {
			f.OracleEvidence[i].RuntimeUnverifiable = true
			f.OracleEvidence[i].RuntimeReason = "clock"
		}
	})
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{unverifiable}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.entryPath("p.A")); err != nil {
		t.Fatalf("unverifiable record not in the overlay: %v", err)
	}
	if err := os.WriteFile(ExemptionsPathFor(path), []byte(`{"version":1,"exemptions":[{"subject":"p.A","reason":"clock","rationale":"reviewed"},{"subject":"p.ATest","reason":"clock","rationale":"reviewed"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Update(ctx, func(current []Finding) ([]Finding, error) { return current, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.entryPath("p.A")); !os.IsNotExist(err) {
		t.Fatalf("the newly committable record kept its overlay entry: %v", err)
	}
	repoData, _ := os.ReadFile(path)
	repo, _ := ParseFindings(repoData)
	if len(repo) != 1 || repo[0].Symbol != "p.A" {
		t.Fatalf("repo layer = %+v; want the exempted record", repo)
	}
}

// The repo document is parsed once per distinct content: the store's
// own commits fill the cache with the rows they wrote, so a run's
// incremental commits and the reads between them parse nothing; the key
// is the content itself, so a foreign rewrite a stat identity could not
// tell apart (same size, same modification time) is still re-parsed —
// the document is every commit's merge base, and a stale prior there
// would lose the foreign rows — while identical bytes rewritten are
// served (REQ-result-layers).
func TestStoreParsesTheDocumentOncePerContent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) {
		return []Finding{storeFinding("p.A", nil), storeFinding("p.B", nil)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	parses := 0
	store.hooks.documentParse = func() { parses++ }
	for range 2 {
		if _, err := store.Load(ctx); err != nil {
			t.Fatal(err)
		}
	}
	changed := storeFinding("p.B", func(f *Finding) { f.BodyHash = "h2" })
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		next := append([]Finding(nil), current...)
		for i := range next {
			if next[i].Symbol == "p.B" {
				next[i] = changed
			}
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	if merged := loadSymbols(t, store); merged["p.B"].BodyHash != "h2" {
		t.Fatalf("the committed change is not served: %+v", merged["p.B"])
	}
	if parses != 0 {
		t.Fatalf("document parses after the store's own writes = %d; want none", parses)
	}
	// A foreign rewrite of the same size at the same modification time.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	foreign := bytes.Replace(data, []byte(`"bodyHash": "h2"`), []byte(`"bodyHash": "h3"`), 1)
	if bytes.Equal(foreign, data) || len(foreign) != len(data) {
		t.Fatal("the foreign rewrite must differ in content only")
	}
	if err := os.WriteFile(path, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("the foreign rewrite must keep the stat identity: %v, %v", after, err)
	}
	if merged := loadSymbols(t, store); merged["p.B"].BodyHash != "h3" {
		t.Fatalf("a foreign rewrite of the same stat identity served stale: %+v", merged["p.B"])
	}
	if parses != 1 {
		t.Fatalf("document parses after the foreign rewrite = %d; want one", parses)
	}
	// Identical bytes rewritten under a new modification time are served.
	if err := os.WriteFile(path, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	loadSymbols(t, store)
	if parses != 1 {
		t.Fatalf("document parses after an identical rewrite = %d; want still one", parses)
	}
}

// The cached document is exactly what a parse of the file yields, over
// every record shape the format canonicalizes (absent and empty lists,
// the optional shape and ledger tables, shared evidence) and across
// changed, unchanged, and skipped rows — so a store write, which
// re-parses only the rows it changed, leaves a readable document
// (REQ-result-export) and serves the same records a fresh reader sees.
func TestStoreCachedDocumentEqualsAFreshParse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	shapes := []func(*Finding){
		nil,
		func(f *Finding) {
			f.Operators = nil
			f.CandidateCount, f.Generated, f.Mutants, f.Killed = 0, 0, 0, 0
		},
		func(f *Finding) {
			f.Operators = []OperatorSummary{}
			f.CandidateCount, f.Generated, f.Mutants, f.Killed = 0, 0, 0, 0
		},
		func(f *Finding) { f.Kills, f.Survivors, f.Attested, f.CandidateEvidence, f.Labels = []Kill{}, []Survivor{}, []Attestation{}, []CandidateEvidence{}, []string{} },
		func(f *Finding) {
			f.Mutants, f.Killed = 2, 1
			f.Operators = []OperatorSummary{{Operator: "zero return", Generated: 2, Killed: 1, Survived: 1}}
			f.Generated, f.CandidateCount = 2, 2
			f.Survivors = []Survivor{{Operator: "zero return", Position: "p.go:1:1"}}
			f.Kills = []Kill{{Operator: "zero return", Position: "p.go:2:1", Killer: "TestA"}}
		},
		func(f *Finding) {
			f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
			f.TargetEvidence = SubjectEvidence{}
		},
		func(f *Finding) {
			f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
			f.TargetEvidence, f.Labels = SubjectEvidence{}, []string{}
		},
		func(f *Finding) { f.CompartmentLedger = &CompartmentLedger{} },
		func(f *Finding) { f.CompartmentLedger = &CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "p.go", Kind: "func", Name: "A", Hash: "h"}}, FileHeaders: []CompartmentFileHeader{}} },
		func(f *Finding) { f.OracleEvidence = append(f.OracleEvidence, cleanEvidence("p.ATest"), cleanEvidence("q.BTest")) },
		func(f *Finding) { f.Cached = true },
		func(f *Finding) { f.Exempted = []Exemption{} },
		func(f *Finding) { f.Exempted = []Exemption{{Subject: "p", Reason: "r", Rationale: "why"}} },
		func(f *Finding) {
			f.Shape = &TargetShape{Manual: &ManualSpec{File: "p.go", Edits: []ManualEdit{{Find: "a", Replace: "b"}}}}
			f.TargetEvidence = SubjectEvidence{}
		},
		func(f *Finding) {
			f.Shape = &TargetShape{Manual: &ManualSpec{File: "p.go"}}
			f.TargetEvidence = SubjectEvidence{}
		},
	}
	documentParses, recordParses := 0, 0
	var lastParsed string
	store.hooks.documentParse = func() { documentParses++ }
	store.hooks.recordParse = func(symbol string) { recordParses++; lastParsed = symbol }
	check := func(step string) {
		t.Helper()
		if documentParses != 0 {
			t.Fatalf("%s: the store re-parsed its own document %d times", step, documentParses)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := ParseFindings(data)
		if err != nil {
			t.Fatalf("%s: the written document does not parse: %v", step, err)
		}
		if bytes.Contains(data, []byte(`"edits": null`)) {
			t.Fatalf("%s: a manual shape's required edit list was written absent; the export writes it present like every required list", step)
		}
		served, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(served, fresh) {
			t.Fatalf("%s: the cached document differs from a fresh parse\nserved: %+v\nfresh:  %+v", step, served, fresh)
		}
	}
	var records []Finding
	for i, shape := range shapes {
		records = append(records, storeFinding(fmt.Sprintf("p.S%d", i), shape))
	}
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return records, nil }); err != nil {
		t.Fatal(err)
	}
	check("first commit")
	if recordParses != len(records) {
		t.Fatalf("first commit parsed %d records; want every new record (%d)", recordParses, len(records))
	}
	// Each record changed in turn, the rest unchanged and re-supplied
	// as served: exactly the changed record is re-parsed. A skipped
	// record persists nothing: the symbol keeps its row.
	for i := range records {
		changed := records[i]
		changed.BodyHash = "h2"
		if i == 0 {
			changed.Skipped = "no tests"
		}
		recordParses = 0
		if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
			next := append([]Finding(nil), current...)
			for j := range next {
				if next[j].Symbol == changed.Symbol {
					next[j] = changed
				} else {
					// An unchanged record re-supplied as served: the
					// never-persisted flag must not reach the cache.
					next[j].Cached = true
				}
			}
			return next, nil
		}); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("commit changing %s", changed.Symbol))
		want := 1
		if changed.Skipped != "" {
			want = 0
		}
		if recordParses != want || (want == 1 && lastParsed != changed.Symbol) {
			t.Fatalf("commit changing %s parsed %d records (last %s); want %d, the changed one", changed.Symbol, recordParses, lastParsed, want)
		}
		if served := loadSymbols(t, store)[changed.Symbol]; changed.Skipped != "" && served.BodyHash != "h" {
			t.Fatalf("a skipped record replaced its symbol's row: %+v", served)
		} else if changed.Skipped == "" && served.BodyHash != "h2" {
			t.Fatalf("the changed record was not committed: %+v", served)
		}
	}
	// A commit re-supplying the merged view unchanged parses nothing;
	// so does one re-supplying the records in their original in-memory
	// form (absent lists, the served flag) — the persisted-form key
	// treats a parsed row as a fixed point.
	inMemory := append([]Finding(nil), records...)
	for i := range inMemory {
		if i > 0 {
			inMemory[i].BodyHash = "h2"
		}
	}
	for _, leg := range []struct {
		name   string
		supply func([]Finding) []Finding
	}{
		{"merged view", func(current []Finding) []Finding { return current }},
		{"in-memory", func([]Finding) []Finding { return inMemory }},
	} {
		recordParses = 0
		if err := store.Update(ctx, func(current []Finding) ([]Finding, error) { return leg.supply(current), nil }); err != nil {
			t.Fatal(err)
		}
		check("unchanged commit from the " + leg.name)
		if recordParses != 0 {
			t.Fatalf("an unchanged commit from the %s parsed %d records", leg.name, recordParses)
		}
	}
}

// A skipped record measured nothing, so a commit carrying one persists
// nothing for its symbol and evicts nothing: the repo row and the
// overlay entry it would have replaced stay, and a skipped new symbol
// leaves no record (REQ-result-export's exclusion, per layer).
func TestStoreSkippedRecordPersistsNothing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	local := storeFinding("p.B", func(f *Finding) { f.Dirty = true })
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{storeFinding("p.A", nil), local}, nil }); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(store.entryPath("p.B"))
	if err != nil {
		t.Fatal(err)
	}
	skipped := func(f Finding) Finding {
		f.BodyHash, f.Skipped = "h2", "no tests"
		return f
	}
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		next := []Finding{skipped(storeFinding("p.C", nil))}
		for _, f := range current {
			next = append(next, skipped(f))
		}
		return next, nil
	}); err != nil {
		t.Fatal(err)
	}
	merged := loadSymbols(t, store)
	if len(merged) != 2 || merged["p.A"].BodyHash != "h" || merged["p.B"].BodyHash != "h" {
		t.Fatalf("skipped records changed the persisted set: %+v", merged)
	}
	if after, err := os.ReadFile(store.entryPath("p.B")); err != nil || !bytes.Equal(after, entry) {
		t.Fatalf("a skipped record rewrote its symbol's overlay entry: %v", err)
	}
	if _, err := os.Stat(store.entryPath("p.C")); !os.IsNotExist(err) {
		t.Fatalf("a skipped new symbol left an overlay entry: %v", err)
	}
	// The serializer itself refuses a skipped record: no caller can
	// persist nothing-measured by another route.
	if _, _, err := persistRecord(skipped(storeFinding("p.D", nil))); err == nil || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("persistRecord on a skipped record: %v; want the refusal", err)
	}
}

// The document served through the content-keyed cache is isolated from
// its callers: a caller's in-place edit of a merged view, or of a
// record it retained from an update's view after the commit, never
// reaches a later read — the store holds the parse for its lifetime,
// so one leak would corrupt every read after it.
func TestStoreDocumentViewIsIsolatedFromCallerMutation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	record := storeFinding("p.A", func(f *Finding) {
		f.Mutants, f.Killed, f.Generated, f.CandidateCount = 2, 1, 2, 2
		f.Operators = []OperatorSummary{{Operator: "zero return", Generated: 2, Killed: 1, Survived: 1}}
		f.Survivors = []Survivor{{Position: "p.go:1:1", Operator: "zero return"}}
		f.Exempted = []Exemption{{Subject: "p", Reason: "r", Rationale: "why"}}
	})
	var retained []Finding
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{record}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		retained = current
		return current, nil
	}); err != nil {
		t.Fatal(err)
	}
	retained[0].Survivors[0].Operator = "leaked through the update's view"
	retained[0].Exempted[0].Subject = "leaked"
	// The record is repo-only here, so a read serves the document
	// cache itself, not an overlay entry shadowing it.
	served, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	served[0].Survivors[0].Operator = "leaked through a read"
	served[0].Exempted[0].Subject = "leaked"
	again, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Survivors[0].Operator != "zero return" || again[0].Exempted[0].Subject != "p" {
		t.Fatalf("a caller's edit reached a later read: %+v", again[0])
	}
	// A repo row kept in place of a machine-local successor is the
	// same path with the row taken from the prior document.
	var kept []Finding
	if err := store.Update(ctx, func(current []Finding) ([]Finding, error) {
		kept = current
		local := cloneFinding(current[0])
		local.Dirty = true
		return []Finding{local}, nil
	}); err != nil {
		t.Fatal(err)
	}
	kept[0].Survivors[0].Operator = "leaked through the kept row"
	if row := store.document.findings[0]; row.Survivors[0].Operator != "zero return" {
		t.Fatalf("a caller's edit reached the cached document through a kept repo row: %+v", row)
	}
}

// A store write validates the rows it changed: a changed record that
// is not serializable refuses the commit with the export's wording and
// leaves the document untouched (REQ-result-export).
func TestStoreWriteRefusesAnInvalidChangedRecord(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{storeFinding("p.A", nil)}, nil }); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	invalid := storeFinding("p.A", func(f *Finding) { f.Killed = 5 })
	err = store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{invalid}, nil })
	if err == nil || !strings.Contains(err.Error(), "export invalid findings") {
		t.Fatalf("invalid changed record: %v; want the export refusal", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused commit changed the document")
	}
}

// mutateEveryList walks a record and edits every list it reaches —
// each string element, and the first string field of each element
// struct — through pointers and nested structs, so a list-bearing
// field added to Finding is covered without a hand-maintained table.
// It returns the number of lists edited.
func mutateEveryList(v reflect.Value) int {
	edited := 0
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			edited += mutateEveryList(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				edited += mutateEveryList(v.Field(i))
			}
		}
	case reflect.Slice:
		if v.Len() == 0 {
			return 0
		}
		edited++
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			switch elem.Kind() {
			case reflect.String:
				elem.SetString("mutated")
			case reflect.Struct:
				for j := 0; j < elem.NumField(); j++ {
					if elem.Field(j).Kind() == reflect.String && elem.Type().Field(j).IsExported() {
						elem.Field(j).SetString("mutated")
						break
					}
				}
				edited += mutateEveryList(elem)
			default:
				edited += mutateEveryList(elem)
			}
		}
	}
	return edited
}

// A served record shares no list with the store's caches, over every
// list the record carries — the caches now outlive a call, so one
// shared list would corrupt every later read; the walk is reflective so
// a list-bearing field added to Finding without its clone fails here
// rather than passing unnoticed.
func TestStoreServedRecordsShareNoListWithTheCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	full := func(f *Finding) {
		f.Labels = []string{"l"}
		f.Mutants, f.Killed, f.Generated, f.CandidateCount, f.Discarded = 3, 1, 4, 4, 1
		f.Operators = []OperatorSummary{{Operator: "zero return", Generated: 4, Discarded: 1, Killed: 1, Survived: 2}}
		f.Kills = []Kill{{Position: "p.go:2:1", Operator: "zero return", Killer: "TestA"}}
		f.Survivors = []Survivor{{Position: "p.go:1:1", Operator: "zero return"}, {Position: "p.go:3:1", Operator: "zero return"}}
		f.Attested = []Attestation{{Position: "p.go:3:1", Operator: "zero return", Reason: "equivalent"}}
		f.CandidateEvidence = []CandidateEvidence{{Position: "p.go:4:1", Operator: "zero return", Reason: "mutant test process timed out", Disposition: "killed"}}
		f.Exempted = []Exemption{{Subject: "p", Reason: "r", Rationale: "why"}}
		f.CompartmentLedger = &CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "p.go", Kind: "func", Name: "A", Hash: "h"}}, FileHeaders: []CompartmentFileHeader{{File: "p.go", Hash: "h"}}}
	}
	records := []Finding{
		storeFinding("p.Structural", func(f *Finding) {
			full(f)
			f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
			f.TargetEvidence = SubjectEvidence{}
		}),
		storeFinding("p.Manual", func(f *Finding) {
			full(f)
			f.Shape = &TargetShape{Manual: &ManualSpec{File: "p.go", Edits: []ManualEdit{{Find: "a", Replace: "b"}}}}
			f.TargetEvidence = SubjectEvidence{}
		}),
		storeFinding("p.Local", func(f *Finding) { full(f); f.Dirty = true }),
	}
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return records, nil }); err != nil {
		t.Fatal(err)
	}
	// Both caches warm: the document's from the write, the overlay's
	// from the install; a second read serves them.
	if _, err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	served, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range served {
		if n := mutateEveryList(reflect.ValueOf(&served[i]).Elem()); n < 10 {
			t.Fatalf("%s: the fixture reaches only %d lists; every list-bearing field must be populated", served[i].Symbol, n)
		}
	}
	again, err := store.Load(ctx)
	if err != nil || len(again) != len(records) {
		t.Fatalf("merged read = %d records, %v; want %d", len(again), err, len(records))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Finding{}
	for _, f := range fresh {
		want[f.Symbol] = f
	}
	for _, f := range again {
		if f.Symbol == "p.Local" {
			continue
		}
		if !reflect.DeepEqual(f, want[f.Symbol]) {
			t.Fatalf("%s: a caller's edit reached the document cache:\nserved: %+v\nfile:   %+v", f.Symbol, f, want[f.Symbol])
		}
	}
	entry, err := os.ReadFile(store.entryPath("p.Local"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseFindings(entry)
	if err != nil || len(local) != 1 {
		t.Fatal(err)
	}
	for _, f := range again {
		if f.Symbol == "p.Local" && !reflect.DeepEqual(f, local[0]) {
			t.Fatalf("a caller's edit reached the overlay cache:\nserved: %+v\nentry:  %+v", f, local[0])
		}
	}
}
