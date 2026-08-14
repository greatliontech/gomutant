package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// A DIR:PATTERN declaration parses into the gofresh namespace form;
// a missing separator or a declaration the namespace grammar refuses
// fails at the boundary, before any measurement (REQ-exec-scratch-namespace).
func TestParseScratchNamespaces(t *testing.T) {
	parsed, err := ParseScratchNamespaces([]string{"scratch:work-*", "sub/dir:tmp"})
	if err != nil || len(parsed) != 2 {
		t.Fatalf("parse = %+v, %v", parsed, err)
	}
	if parsed[0] != (runtimeinput.ScratchNamespace{Dir: "scratch", Pattern: "work-*"}) {
		t.Fatalf("parsed[0] = %+v", parsed[0])
	}
	for name, entry := range map[string]string{
		"missing separator": "scratchwork",
		"empty dir":         ":work-*",
		"escaping dir":      "../out:work-*",
		"multi-component":   "scratch:a/b",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseScratchNamespaces([]string{entry}); err == nil {
				t.Fatalf("malformed declaration %q accepted", entry)
			}
		})
	}
}

// A malformed namespace reaching Options directly (a library caller
// bypassing the flag parser) still refuses at run start, before any
// measurement (REQ-exec-scratch-namespace).
func TestRunRefusesMalformedScratchNamespace(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/nsrefuse\n\ngo 1.26.4\n",
		"p.go":   "package nsrefuse\n\nfunc F(x int) int { return x }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), nil, Options{
		ScratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "../out", Pattern: "p"}},
	})
	if err == nil || !strings.Contains(err.Error(), "refused before measurement") {
		t.Fatalf("err = %v, want the run-start refusal", err)
	}
}

// A declared bracket path absent or unhashable against the measured
// module's root refuses before that module's first spawn - the field
// failure this pins burned a 100-candidate measurement on a transient
// per-test directory before failing at finalization
// (REQ-exec-observation). The preflight fires at group formation, so a
// real target drives it.
func TestRunRefusesAbsentBracketPath(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/preflight\n\ngo 1.26.4\n",
		"p.go":      "package preflight\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package preflight\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := []Target{{Symbol: "example.com/preflight.F", Oracle: []string{"example.com/preflight.TestF"}, OracleExplicit: true}}
	opts := func(paths ...string) Options {
		return Options{Budget: 1, OracleTimeout: 2 * time.Minute, BracketPaths: paths}
	}
	_, err = tree.Run(context.Background(), target, opts("transient/per-test"))
	if err == nil || !strings.Contains(err.Error(), "does not exist at run start") {
		t.Fatalf("err = %v, want the preflight refusal", err)
	}
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(dir, "sealed")
		if err := os.MkdirAll(filepath.Join(unreadable, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(unreadable, 0o755)
		_, err = tree.Run(context.Background(), target, opts("sealed"))
		if err == nil || !strings.Contains(err.Error(), "preflight") {
			t.Fatalf("err = %v, want the unhashable-surface refusal", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "transient", "per-test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "transient", "per-test", "fixture"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := tree.Run(context.Background(), target, opts("transient/per-test"))
	if err != nil || len(findings) != 1 {
		t.Fatalf("existing declared surface refused: %+v, %v", findings, err)
	}
}

// In-module scratch minted and removed inside a declared namespace
// stops recording per-run missing-arm identities - the manifest carries
// no random-named scratch entry, restoring union-equality across runs -
// while the same oracle without the declaration records the minted name
// (REQ-exec-scratch-namespace).
func TestScratchNamespaceOracleScratchRecordless(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	build := func() *Tree {
		dir := t.TempDir()
		files := map[string]string{
			"go.mod":        "module example.com/nsscratch\n\ngo 1.26.4\n",
			"scratch/.keep": "",
			"p.go":          "package nsscratch\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
			"p_test.go": `package nsscratch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestF(t *testing.T) {
	// The absence probe below is the record the namespace admission
	// drops: minting machinery probes candidate names before creating.
	if _, err := os.Stat(filepath.Join("scratch", "work-probe")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	d, err := os.MkdirTemp("scratch", "work-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	f := filepath.Join(d, "data")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(f); err != nil {
		t.Fatal(err)
	}
	if F(5) != 5 {
		t.Fatal()
	}
}
`,
		}
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tree, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	target := Target{Symbol: "example.com/nsscratch.F", Oracle: []string{"example.com/nsscratch.TestF"}, OracleExplicit: true}
	scratchRecords := func(opts Options) []string {
		findings, err := build().Run(context.Background(), []Target{target}, opts)
		if err != nil || len(findings) != 1 {
			t.Fatalf("measure = %+v, %v", findings, err)
		}
		evidence := findings[0].OracleEvidence[0]
		if evidence.RuntimeUnverifiable {
			t.Fatalf("oracle evidence unverifiable: %q", evidence.RuntimeReason)
		}
		// Finding evidence is finalized with absolute identities for
		// cross-module reuse, so the filter matches the absolute form.
		paths, err := runtimeinput.Paths(evidence.RuntimeInputs, ".")
		if err != nil {
			t.Fatal(err)
		}
		var scratch []string
		for _, p := range paths {
			if strings.Contains(p, string(filepath.Separator)+"scratch"+string(filepath.Separator)+"work-") {
				scratch = append(scratch, p)
			}
		}
		return scratch
	}
	declared := scratchRecords(Options{
		Budget: 1, OracleTimeout: 2 * time.Minute,
		ScratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "scratch", Pattern: "work-*"}},
	})
	if len(declared) != 0 {
		t.Fatalf("declared namespace still records scratch identities: %v", declared)
	}
	undeclared := scratchRecords(Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if len(undeclared) == 0 {
		t.Fatal("undeclared scratch recorded nothing; the declared arm above proves nothing")
	}
}

// Bracket-path preflight resolves against the measured module's root -
// the base each spawn's capture uses - so a workspace member's
// module-relative declaration passes although no such path exists at
// the workspace root (REQ-exec-observation).
func TestWorkspaceMemberBracketPathPreflightsAgainstModuleRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.work":             "go 1.26.4\n\nuse ./m\n",
		"m/go.mod":            "module example.com/m\n\ngo 1.26.4\n",
		"m/fixtures/data.txt": "fixed\n",
		"m/p.go":              "package m\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"m/p_test.go":         "package m\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/m.F", Oracle: []string{"example.com/m.TestF"}, OracleExplicit: true}},
		Options{Budget: 1, OracleTimeout: 2 * time.Minute, BracketPaths: []string{"fixtures"}})
	if err != nil || len(findings) != 1 {
		t.Fatalf("member-module declaration refused: %+v, %v", findings, err)
	}
}
