package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// TestFindingsAtAndUpdate pins the CLI's document anchoring (a relative
// findings path anchors at the tree root, matching the MCP face) and the
// locked update round trip it shares with the server.
func TestFindingsAtAndUpdate(t *testing.T) {
	dir := t.TempDir()
	path := findingsAt(dir, defaultFindings)
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("default document not anchored at the tree: %s", path)
	}
	abs := filepath.Join(t.TempDir(), "f.json")
	if findingsAt(dir, abs) != abs {
		t.Fatal("absolute findings path rewritten")
	}
	fresh := []gomutant.Finding{{Symbol: "p.A", BodyHash: "h", OperatorSet: "go/2", Mutants: 1, Killed: 1}}
	err := gomutant.UpdateDocument(path, func(prior []gomutant.Finding) ([]gomutant.Finding, error) {
		return gomutant.MergeFindings(prior, fresh), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadFindings(path)
	if err != nil || len(got) != 1 || got[0].Symbol != "p.A" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestCobraCommandTree(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("root help missing usage:\n%s", output.String())
	}
	for _, command := range []string{"run", "findings", "attest", "ephemeral", "mcp"} {
		found := false
		for _, child := range root.Commands() {
			found = found || child.Name() == command
		}
		if !found {
			t.Fatalf("root command tree omits %q", command)
		}
	}

	root = newRootCommand()
	root.SetArgs([]string{"attest"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--symbol") {
		t.Fatalf("missing attest flags = %v", err)
	}

	root = newRootCommand()
	root.SetArgs([]string{"run", "unexpected"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("positional argument accepted: %v", err)
	}

	if err := run(nil); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("empty invocation = %v", err)
	}
	got := normalizeLegacyFlags([]string{"attest", "--reason", "-force", "-symbol=x", "--", "-timeout"})
	want := []string{"attest", "--reason", "-force", "--symbol=x", "--", "-timeout"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("legacy flags = %q, want %q", got, want)
	}
}
