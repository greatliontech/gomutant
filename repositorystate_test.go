package gomutant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/gitfixture"
)

// mustRepositoryState is the tests' capture of a directory's repository
// state through the one production capture, faults fatal — the deleted
// wrapper swallowed them.
func mustRepositoryState(t *testing.T, dir string) repositoryState {
	t.Helper()
	state, err := captureRepositoryStateContext(context.Background(), dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// historicalFiles is the tests' single-valued read of a repository
// state's historical package files, faults fatal — the deleted wrapper
// swallowed them.
func historicalFiles(t *testing.T, s repositoryState, sourceFiles []string) []string {
	t.Helper()
	paths, unlisted, err := s.historicalPackageFilesContext(context.Background(), sourceFiles)
	if err != nil || len(unlisted) != 0 {
		t.Fatalf("historical files: %v; unlisted %q", err, unlisted)
	}
	return paths
}

// subjectViewOf is the tests' single-subject view through the one
// production constructor.
func subjectViewOf(tr *Tree, symbol string) (*subjectView, error) {
	views, err := tr.newSubjectViews(context.Background(), []string{symbol}, false, engine.OracleBounds{})
	if err != nil {
		return nil, err
	}
	return views.bySymbol[symbol], nil
}

// A historical listing that fails says nothing about the files HEAD
// holds under a package directory. The read names the directory
// unlisted — never a shorter pathspec, which would stamp a deleted
// package file clean, and never an error, which would abort the
// campaign — and the stamp resolves the fact target-locally
// (REQ-exec-quiescence): the no-commit-provenance posture (commit
// omitted, dirty) on a plain run; a staged run refuses the target with
// the failure named, at the stamp and at the external-input judgment.
// A git on the parent's PATH failing `ls-tree` alone stands in for the
// failure; off PATH, the same read lists the committed file.
func TestHistoricalListingFailureIsAStampFact(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a repository and loads its tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.26\n",
		"lib/a.go":      "package lib\n\nfunc F() int { return 1 }\n",
		"lib/a_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := gitfixture.Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		cmd, err := gitfixture.Command(dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	view, err := subjectViewOf(tr, "example.com/m/lib.F")
	if err != nil {
		t.Fatal(err)
	}
	plain := mustRepositoryState(t, dir)
	staged, err := captureRepositoryStateContext(ctx, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "lib", "a.go")
	if got := historicalFiles(t, plain, []string{source}); len(got) != 2 {
		t.Fatalf("historical files = %q; want the two committed package files", got)
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = ls-tree ]; then echo boom >&2; exit 1; fi\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	paths, unlisted, err := plain.historicalPackageFilesContext(ctx, []string{source})
	if err != nil || len(paths) != 0 || len(unlisted) != 1 || !strings.Contains(unlisted[0], "historical package files under lib unlisted: git ls-tree") || !strings.Contains(unlisted[0], "boom") {
		t.Fatalf("a failed listing = %q, %q, %v; want the fact naming the directory, the listing and its stderr, no error", paths, unlisted, err)
	}
	// The plain stamp: the no-commit posture, no error.
	f := Finding{Symbol: "example.com/m/lib.F"}
	drift, err := tr.stampProvenance(ctx, plain, view, nil, nil, &f)
	if err != nil || drift != "" || !f.Dirty || f.Commit != "" {
		t.Fatalf("the plain stamp under a failed listing = drift %q, err %v, dirty %v, commit %q; want the no-commit posture", drift, err, f.Dirty, f.Commit)
	}
	// The staged stamp refuses the target naming the failure.
	f = Finding{Symbol: "example.com/m/lib.F"}
	drift, err = tr.stampProvenance(ctx, staged, view, nil, nil, &f)
	if err != nil || !strings.Contains(drift, "historical package files under lib unlisted") || !strings.Contains(drift, "the staged snapshot cannot vouch for the target") {
		t.Fatalf("the staged stamp under a failed listing = drift %q, err %v; want the refusal naming the listing", drift, err)
	}
	// The staged external-input judgment names it the same way.
	if _, unlisted, err := tr.externalInputs(ctx, staged, view, nil, nil); err != nil || len(unlisted) != 1 || !strings.Contains(unlisted[0], "boom") {
		t.Fatalf("the external-input judgment under a failed listing = %q, %v; want the listing named", unlisted, err)
	}
}
