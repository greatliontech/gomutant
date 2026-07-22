package gomutant

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
	return SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build",
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
