package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func selectionTree(t *testing.T) *Tree {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":      "module example.com/sel\n\ngo 1.26.4\n",
		"sel.go":      "package sel\n\nfunc Value() int { return 1 }\n\nfunc Other() int { return 2 }\n",
		"sel_test.go": "package sel\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n\nfunc TestOther(t *testing.T) { if Other() != 2 { t.Fatal() } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// The one dispatch: a selection is the whole tree exactly when no
// source and no filter narrowed it; a targets document — empty
// included — is a source; a changed ref cuts only when asked to
// (REQ-result-hygiene, REQ-exec-run-status).
func TestSelectTargetsIsTheOneDispatch(t *testing.T) {
	tree := selectionTree(t)
	ctx := context.Background()
	whole, err := tree.SelectTargets(ctx, SelectionRequest{})
	if err != nil || !whole.WholeTree || len(whole.Targets) != 2 || whole.Cut != nil {
		t.Fatalf("whole tree = %+v, %v", whole, err)
	}
	filtered, err := tree.SelectTargets(ctx, SelectionRequest{Symbols: []string{"example.com/sel.Value"}})
	if err != nil || filtered.WholeTree || len(filtered.Targets) != 1 {
		t.Fatalf("filtered = %+v, %v; a filter narrows the whole tree", filtered, err)
	}
	empty, err := tree.SelectTargets(ctx, SelectionRequest{TargetsGiven: true})
	if err != nil || empty.WholeTree || len(empty.Targets) != 0 {
		t.Fatalf("empty document = %+v, %v; a given document is a source", empty, err)
	}
	// The filter walk runs over the document producer too: two targets
	// given, the filter keeps one.
	document := []Target{
		{Symbol: "example.com/sel.Other", Oracle: []string{"example.com/sel.TestOther"}, OracleExplicit: true},
		{Symbol: "example.com/sel.Value", Oracle: []string{"example.com/sel.TestValue"}, OracleExplicit: true},
	}
	given, err := tree.SelectTargets(ctx, SelectionRequest{TargetsGiven: true, Targets: document, Symbols: []string{"example.com/sel.Other"}})
	if err != nil || given.WholeTree || len(given.Targets) != 1 || given.Targets[0].Symbol != "example.com/sel.Other" {
		t.Fatalf("given document, filtered = %+v, %v", given, err)
	}
	changed := &ChangedSelection{Surface: ChangedSurface{Ref: "HEAD", Paths: []string{"sel.go"}, Added: map[string][]LineRange{"sel.go": {{From: 3, To: 4}}}}, Ref: func(string) ([]byte, bool) { return nil, false }}
	// The filter walk runs over the changed producer too.
	uncut, err := tree.SelectTargets(ctx, SelectionRequest{Changed: changed, Symbols: []string{"example.com/sel.Other"}})
	if err != nil || uncut.WholeTree || uncut.Cut != nil || len(uncut.Targets) != 1 || uncut.Targets[0].Symbol != "example.com/sel.Other" {
		t.Fatalf("changed, uncut, filtered = %+v, %v", uncut, err)
	}
	// A changed selection without its content reader is refused, never
	// dereferenced.
	if _, err := tree.SelectTargets(ctx, SelectionRequest{Changed: &ChangedSelection{Surface: changed.Surface}}); err == nil {
		t.Fatal("a reader-less changed selection was dispatched")
	}
	cut, err := tree.SelectTargets(ctx, SelectionRequest{Changed: changed, Cut: true})
	if err != nil || cut.Cut == nil || cut.Cut.Ref != "HEAD" || !cut.Cut.OnDelta("sel.go", 3) || len(cut.Targets) != 2 {
		t.Fatalf("changed, cut = %+v, %v; want the cut from the one surface", cut, err)
	}
}
