package gomutant

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
	if ok, reason := Committable(storeFinding("p.A", nil), dir); !ok {
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
		if ok, reason := Committable(storeFinding("p.A", tc.mutate), dir); ok || !strings.Contains(reason, tc.want) {
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
	if reasons := CommittableReasons(storeFinding("p.A", nil), dir); len(reasons) != 0 {
		t.Fatalf("clean finding carries reasons: %v", reasons)
	}
	shared := storeManifest("/etc/hosts")
	f := storeFinding("p.A", func(f *Finding) {
		f.Dirty = true
		f.Commit = ""
		f.TargetEvidence.RuntimeInputs = shared
		f.OracleEvidence[0].RuntimeUnverifiable = true
		f.OracleEvidence[0].RuntimeInputs = shared
	})
	want := []string{
		"dirty worktree provenance",
		"no commit provenance",
		"machine-local runtime input /etc/hosts",
		"runtime-unverifiable evidence for p.ATest",
	}
	reasons := CommittableReasons(f, dir)
	if len(reasons) != len(want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
	for i := range want {
		if reasons[i] != want[i] {
			t.Fatalf("reasons[%d] = %q, want %q (full: %v)", i, reasons[i], want[i], reasons)
		}
	}
	if ok, first := Committable(f, dir); ok || first != reasons[0] {
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
	reasons = CommittableReasons(torn, dir)
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
