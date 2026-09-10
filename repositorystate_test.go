package gomutant

import (
	"context"
	"testing"
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
	paths, err := s.historicalPackageFilesContext(context.Background(), sourceFiles)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// subjectViewOf is the tests' single-subject view through the one
// production constructor.
func subjectViewOf(tr *Tree, symbol string) (*subjectView, error) {
	views, err := tr.newSubjectViews(context.Background(), []string{symbol}, false, 0)
	if err != nil {
		return nil, err
	}
	return views.bySymbol[symbol], nil
}
