package gomutant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// workspaceFixture builds a committed go.work workspace: a root module
// holding a fixture the nested member's test reads through a relative
// spelling ("../shared/fixture.txt"), the field shape behind chunk 138's
// reports. It returns the tree root, the member directory, and a
// function committing the tree's current state.
func workspaceFixture(t *testing.T) (string, string, func()) {
	t.Helper()
	root := t.TempDir()
	member := filepath.Join(root, "tools")
	files := map[string]string{
		"go.work":            "go 1.26.4\n\nuse (\n\t.\n\t./tools\n)\n",
		"go.mod":             "module example.com/root\n\ngo 1.26.4\n",
		"root.go":            "package root\n\nfunc Root() int { return 1 }\n",
		"shared/fixture.txt": "fixture\n",
		"tools/go.mod":       "module example.com/root/tools\n\ngo 1.26.4\n",
		"tools/t.go":         "package tools\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"tools/t_test.go":    "package tools\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadFile(\"../shared/fixture.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
		// A test-only package: no plain variant carries its directory.
		"tools/only/only_test.go": "package only\n\nimport \"testing\"\n\nfunc TestOnly(t *testing.T) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	commit := func() {
		runGit("add", "-A")
		runGit("commit", "-q", "--allow-empty", "-m", "base")
	}
	commit()
	return root, member, commit
}

// A relative bracket path resolves against the tree root — one declared
// surface for every module's oracles — so a workspace member's test
// reading a root-module fixture through its own relative spelling
// verifies (the read binds through the bracket, never "not covered"),
// its identity records tree-relative, and the record is portable repo
// evidence, never machine-local (REQ-exec-observation,
// REQ-result-layers). Undeclared, the same read seals the observation.
func TestWorkspaceMemberBracketPathResolvesAgainstTheTreeRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant over a workspace fixture")
	}
	root, _, _ := workspaceFixture(t)
	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/root/tools.F", Oracle: []string{"example.com/root/tools.TestF"}, OracleExplicit: true}
	findings, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: 2 * time.Minute, BracketPaths: []string{"shared/fixture.txt"}})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	f := findings[0]
	for _, ev := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if ev.RuntimeUnverifiable {
			t.Fatalf("%s: the declared root-module read sealed the observation: %s", ev.Symbol, ev.RuntimeReason)
		}
		if ev.ModuleBase != "" {
			t.Fatalf("%s: tree-anchored evidence carries a module base %q", ev.Symbol, ev.ModuleBase)
		}
	}
	paths, err := runtimeinput.Paths(f.OracleEvidence[0].RuntimeInputs, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, filepath.Join(root, "shared", "fixture.txt")) {
		t.Fatalf("the root fixture is not recorded as a tree-relative identity: %v", paths)
	}
	if f.Dirty {
		t.Fatal("a committed tree stamped dirty")
	}
	if ok, reason := Committable(f, root, nil); !ok {
		t.Fatalf("the workspace member's record is not portable: %s", reason)
	}

	// A serve extension over the member's record splices its recorded
	// union at the base the record is anchored at: the extended record
	// keeps verifiable evidence — a base mismatch would false-diverge it
	// into a non-reusable stamp (REQ-result-stale).
	doc, err := Export(findings)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	extended, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 2, OracleTimeout: 2 * time.Minute, BracketPaths: []string{"shared/fixture.txt"}, Prior: prior,
		Decision: func(d RunDecision) { decisions = append(decisions, d) }})
	if err != nil || len(extended) != 1 {
		t.Fatalf("extension = %+v, %v", extended, err)
	}
	if len(decisions) != 1 || decisions[0].Action != "measure" || !strings.Contains(decisions[0].Reason, "prefix of 1 candidate stands") {
		t.Fatalf("extension decision = %+v, want the served prefix extended", decisions)
	}
	if ev := extended[0].TargetEvidence; ev.RuntimeUnverifiable {
		t.Fatalf("the extended member record false-diverged: %s", ev.RuntimeReason)
	}
	if extended[0].Generated != 2 {
		t.Fatalf("the extension measured %d candidates, want the served prefix plus one", extended[0].Generated)
	}
	if ok, reason := Committable(extended[0], root, nil); !ok {
		t.Fatalf("the extended member record is not portable: %s", reason)
	}

	undeclared, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(undeclared) != 1 {
		t.Fatalf("undeclared measure = %+v, %v", undeclared, err)
	}
	if ev := undeclared[0].OracleEvidence[0]; !ev.RuntimeUnverifiable || !strings.Contains(ev.RuntimeReason, "not covered by observation bracket") {
		t.Fatalf("undeclared root-module read did not seal the observation: %+v", ev)
	}
}

// An absolute directory is an admissible bracket path: a replace module
// outside the repository is one declared surface the bracket walks, so
// a read beneath it binds — and, being outside the repository, keeps
// the record machine-local (REQ-exec-observation, REQ-result-layers).
func TestAbsoluteDirectoryBracketPathIsOneDeclaredSurface(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, member, commit := workspaceFixture(t)
	external := filepath.Join(t.TempDir(), "replace")
	if err := os.MkdirAll(filepath.Join(external, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "sub", "data.txt"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	test := "package tools\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadFile(" + "`" + filepath.Join(external, "sub", "data.txt") + "`" + "); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(member, "t_test.go"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	commit()
	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/root/tools.F", Oracle: []string{"example.com/root/tools.TestF"}, OracleExplicit: true}
	if err := validateBracketPaths(root, []string{external}); err != nil {
		t.Fatalf("an absolute directory bracket path was refused at validation: %v", err)
	}
	findings, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: 2 * time.Minute, BracketPaths: []string{external}})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	if ev := findings[0].OracleEvidence[0]; ev.RuntimeUnverifiable {
		t.Fatalf("a read under the declared external directory sealed the observation: %s", ev.RuntimeReason)
	}
	if ok, reason := Committable(findings[0], root, nil); ok || !strings.Contains(reason, "machine-local runtime input") {
		t.Fatalf("an external identity kept the record portable: %v %q", ok, reason)
	}
}

// A relative bracket path that escapes the tree names nothing the
// bracket can cover and is refused at validation; the preflight refuses
// an absent surface and one the bracket cannot fingerprint (a root
// containing a volatile OS root) before any measurement
// (REQ-exec-preparation, REQ-exec-observation).
func TestBracketPathValidationAndPreflightRefuseBeforeMeasurement(t *testing.T) {
	root, _, _ := workspaceFixture(t)
	if err := validateBracketPaths(root, []string{"../outside.txt"}); err == nil || !strings.Contains(err.Error(), "escapes the tree root") {
		t.Fatalf("escaping relative path: %v", err)
	}
	if err := validateBracketPaths(root, []string{filepath.Join(root, ".gomutant", "findings.json")}); err == nil || !strings.Contains(err.Error(), "tool-excluded") {
		t.Fatalf("absolute in-tree tool-excluded path: %v", err)
	}
	// The tree reached through a symlinked prefix spells its own files
	// under the resolved root too: still the relative declaration.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBracketPaths(link, []string{filepath.Join(resolvedRoot, ".gomutant", "findings.json")}); err == nil || !strings.Contains(err.Error(), "tool-excluded") {
		t.Fatalf("resolved-root spelling under a symlinked tree root: %v", err)
	}
	// And the mirror: the tree given canonically, the path spelled
	// through the link.
	if err := validateBracketPaths(resolvedRoot, []string{filepath.Join(link, ".gomutant", "findings.json")}); err == nil || !strings.Contains(err.Error(), "tool-excluded") {
		t.Fatalf("linked spelling under a canonical tree root: %v", err)
	}
	if err := preflightBracketPaths(context.Background(), root, []string{"shared/missing.txt"}); err == nil || !strings.Contains(err.Error(), "does not exist at run start") {
		t.Fatalf("absent surface: %v", err)
	}
	if err := preflightBracketPaths(context.Background(), root, []string{"shared/fixture.txt"}); err != nil {
		t.Fatalf("a present tree-relative surface refused: %v", err)
	}
	if err := preflightBracketPaths(context.Background(), root, []string{string(filepath.Separator)}); err == nil || !strings.Contains(err.Error(), "preflight refused") {
		t.Fatalf("the filesystem root as a bracket path: %v", err)
	}
}

// ephemeral accepts the oracle package spelled as a directory the way
// `go test` does ("." or "./x"), resolved against the tree root; an
// import path still names it, and a directory holding no loaded
// package or escaping the tree refuses before any process
// (REQ-exec-ephemeral).
func TestEphemeralTestPackageDirectoryShorthand(t *testing.T) {
	root, _, _ := workspaceFixture(t)
	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for spelling, want := range map[string]string{"./tools": "example.com/root/tools", ".": "example.com/root", "example.com/root/tools": "example.com/root/tools", "./tools/only": "example.com/root/tools/only"} {
		got, err := tr.resolveTestPackage(spelling)
		if err != nil || got != want {
			t.Fatalf("resolveTestPackage(%q) = %q, %v; want %q", spelling, got, err, want)
		}
	}
	for spelling, want := range map[string]string{"./shared": "holds no loaded package", "../elsewhere": "escapes the tree root", "example.com/nowhere": "not a loaded package import path"} {
		if _, err := tr.resolveTestPackage(spelling); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("resolveTestPackage(%q) = %v; want a refusal containing %q", spelling, err, want)
		}
	}
}

// A scratch namespace is tree-relative by the same rule as a bracket
// path: a workspace member's scratch directory is declared by its
// tree-relative path, and under that declaration the member's minted
// scratch records nothing; the member-relative spelling declares a
// directory at the root and the scratch records
// (REQ-exec-scratch-namespace).
func TestWorkspaceMemberScratchNamespaceIsTreeRelative(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, member, commit := workspaceFixture(t)
	test := `package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestF(t *testing.T) {
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
`
	if err := os.MkdirAll(filepath.Join(member, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(member, "scratch", ".keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(member, "t_test.go"), []byte(test), 0o644); err != nil {
		t.Fatal(err)
	}
	commit()
	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/root/tools.F", Oracle: []string{"example.com/root/tools.TestF"}, OracleExplicit: true}
	scratchRecords := func(namespaces []runtimeinput.ScratchNamespace) []string {
		findings, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: 2 * time.Minute, ScratchNamespaces: namespaces})
		if err != nil || len(findings) != 1 {
			t.Fatalf("measure = %+v, %v", findings, err)
		}
		evidence := findings[0].OracleEvidence[0]
		if evidence.RuntimeUnverifiable {
			t.Fatalf("oracle evidence unverifiable: %q", evidence.RuntimeReason)
		}
		paths, err := runtimeinput.Paths(evidence.RuntimeInputs, root)
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
	if declared := scratchRecords([]runtimeinput.ScratchNamespace{{Dir: "tools/scratch", Pattern: "work-*"}}); len(declared) != 0 {
		t.Fatalf("tree-relative namespace still records the member's scratch: %v", declared)
	}
	if memberRelative := scratchRecords([]runtimeinput.ScratchNamespace{{Dir: "scratch", Pattern: "work-*"}}); len(memberRelative) == 0 {
		t.Fatal("the member-relative spelling admitted the member's scratch; a namespace resolves at the tree root")
	}
}

// A workspace member's record splices undiverged at the base its
// evidence is anchored at — the tree — so a serve extension over a
// member target whose oracle reads an in-tree input never
// false-diverges into a non-reusable stamp (REQ-result-stale).
func TestApplySplicedUnionAnchorsAWorkspaceMemberAtTheTree(t *testing.T) {
	root, _, _ := workspaceFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(root, "tools")
	env := tree.eng.GoEnv()
	rel, err := runtimeinput.FromTestLogEnv([]byte("open ../shared/fixture.txt\n"), root, member, env, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	relState, err := runtimeinput.CompletedState(rel)
	if err != nil {
		t.Fatal(err)
	}
	union, err := runtimeinput.AbsoluteEnv(rel, root, env)
	if err != nil {
		t.Fatal(err)
	}
	evidence := SubjectEvidence{Symbol: "example.com/root/tools.F", RuntimeInputs: relState.Manifest, RuntimeDigest: relState.Digest}
	rec := Finding{TargetEvidence: evidence, OracleEvidence: []SubjectEvidence{evidence}}
	_, same, err := tree.applySplicedUnion(context.Background(), env, rec, union, newPortableUnion(union, env), evidenceBase(tree.dir, rec.TargetEvidence))
	if err != nil {
		t.Fatal(err)
	}
	if same.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("the member's tree-anchored record false-diverged: %+v", same.TargetEvidence)
	}
	if same.TargetEvidence.RuntimeInputs != relState.Manifest {
		t.Fatalf("undiverged splice rewrote the recorded manifest: %+v", same.TargetEvidence)
	}
	// Judged at the member instead, the same record reads diverged: the
	// two bases are not interchangeable.
	_, diverged, err := tree.applySplicedUnion(context.Background(), env, rec, union, newPortableUnion(union, env), member)
	if err != nil {
		t.Fatal(err)
	}
	if !diverged.TargetEvidence.RuntimeUnverifiable {
		t.Fatal("a member-based judgment of a tree-anchored record read undiverged; the contrast proves nothing")
	}
}
