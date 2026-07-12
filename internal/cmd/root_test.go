package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

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
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	fresh := []gomutant.Finding{{Symbol: "p.A", BodyHash: "h", OperatorSet: "go/2", Timeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("p.A"), OracleEvidence: []gomutant.SubjectEvidence{evidence("p.TestA")}, Mutants: 1, Killed: 1}}
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

func TestRunCommandWholeTreePrunesWhenNoTargetsRemain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	seed := gomutant.Finding{Symbol: "example.com/empty.Old", BodyHash: "body", OperatorSet: "go/2", Timeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/empty.Old"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestOld")}}
	path := findingsAt(dir, defaultFindings)
	if err := gomutant.UpdateDocument(path, func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{seed}, nil }); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(runOptions{dir: dir, findingsFile: defaultFindings, targetsFile: targetsPath}); err != nil {
		t.Fatal(err)
	}
	retained, err := loadFindings(path)
	if err != nil || len(retained) != 1 {
		t.Fatalf("scoped zero-target run pruned findings: %+v, %v", retained, err)
	}
	if err := runCommand(runOptions{dir: dir, findingsFile: defaultFindings}); err != nil {
		t.Fatal(err)
	}
	got, err := loadFindings(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("whole-tree empty discovery retained findings: %+v, %v", got, err)
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
	if err := Execute(nil); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("empty invocation = %v", err)
	}
	if err := Execute([]string{"run", "-budget", "1"}); err == nil || !strings.Contains(err.Error(), "unknown shorthand") {
		t.Fatalf("single-dash long flag accepted: %v", err)
	}
}
