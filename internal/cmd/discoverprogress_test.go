package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The discover verb's human face names the preparation, the load's
// event, and the selection under the cadence and ends on its rows;
// the JSON face stays a document (REQ-exec-run-status).
func TestDiscoverHumanFaceNamesItsStretches(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	fastCadence(t)
	dir := isolatedFixture(t)
	labels := observeStretches(t)
	var human slowWriter
	if err := discoverCommand(context.Background(), discoverOptions{dir: dir, output: &human}); err != nil {
		t.Fatal(err)
	}
	wantStretchesInOrder(t, labels(), []string{gomutant.StretchPreparation, gomutant.StretchPreparing(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading}), gomutant.StretchSelecting})
	wantLoadingLineAheadOfTheRows(t, human.String())
	seams.stretchObserver = nil
	var doc bytes.Buffer
	if err := discoverCommand(context.Background(), discoverOptions{dir: dir, json: true, output: &doc}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(doc.Bytes()) || strings.Contains(doc.String(), "prepare") {
		t.Fatalf("discover --json under the cadence is not a document: %q", doc.String())
	}
}
