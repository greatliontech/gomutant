package gomutant

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
)

func relManifest(paths ...string) string {
	entries := make([]string, len(paths))
	for i, p := range paths {
		entries[i] = fmt.Sprintf(`{"k":"rel","p":%q,"d":"0123456789abcdef0123456789abcdef"}`, p)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"paths":[` + strings.Join(entries, ",") + `]}`))
}

// A record from before evidence anchored at the tree carries its
// member module base, and the portable line honors it: that subject's
// manifest resolves against the member, so an identity escaping the
// member refuses committability even inside the tree, a member-local
// identity passes; a record without a base — every record anchored at
// the tree — resolves at the tree root (REQ-result-layers).
func TestCommittableResolvesEachSubjectAgainstItsModuleBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "m"), 0o755); err != nil {
		t.Fatal(err)
	}

	memberLocal := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.ModuleBase = "m"
		f.TargetEvidence.RuntimeInputs = relManifest("data.txt")
	})
	if ok, reason := Committable(memberLocal, dir, nil); !ok {
		t.Fatalf("member-local identity refused: %s", reason)
	}

	// A relative identity cannot encode an escape (the manifest refuses
	// dot-dot identities), so the escaping direction is an absolute
	// identity inside the tree but outside the member module: under the
	// member base it refuses; under the baseless tree-root walk the same
	// identity read as portable - the mislocated pass the base closes.
	escaping := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.ModuleBase = "m"
		f.TargetEvidence.RuntimeInputs = storeManifest(filepath.Join(dir, "shared.txt"))
	})
	if ok, reason := Committable(escaping, dir, nil); ok || !strings.Contains(reason, "machine-local runtime input") {
		t.Fatalf("member-escaping identity = committable=%v reason=%q, want the machine-local refusal", ok, reason)
	}

	// Without a base the manifest is tree-anchored: a tree-relative
	// identity anywhere in the tree is repo evidence, an absolute one
	// outside the tree is machine-local.
	anchored := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.RuntimeInputs = relManifest("m/data.txt", "shared.txt")
	})
	if ok, reason := Committable(anchored, dir, nil); !ok {
		t.Fatalf("tree-anchored record refused: %s", reason)
	}
	external := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.RuntimeInputs = storeManifest(filepath.Join(t.TempDir(), "outside.txt"))
	})
	if ok, reason := Committable(external, dir, nil); ok || !strings.Contains(reason, "machine-local runtime input") {
		t.Fatalf("external identity = committable=%v reason=%q, want the machine-local refusal", ok, reason)
	}
}

// The field reproducer for the false-clean portable row: a module below
// the repository root measures a target whose oracle reads an untracked
// runtime input. Manifest identities are module-relative; resolved
// against the git toplevel they materialize at a path that does not
// exist, git reports nothing, and the record lands portable claiming
// provenance its inputs don't have. Resolved against the subject's own
// module the untracked input is seen and the record stamps dirty
// (REQ-result-layers, REQ-result-stale).
func TestFreshMeasureStampsDirtyForUntrackedInputBelowRepositoryRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root := t.TempDir()
	module := filepath.Join(root, "mod")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":    "module example.com/mono\n\ngo 1.26.4\n",
		"p.go":      "package mono\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package mono\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadFile(\"data.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(module, name), []byte(content), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.4\n\nuse ./mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")
	// The runtime input exists but is untracked: the exact false-clean
	// shape - a clean source tree over a dirty input.
	if err := os.WriteFile(filepath.Join(module, "data.txt"), []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The tree loads at the workspace root, so the member module sits
	// below both the repository root and the tree root.
	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/mono.F", Oracle: []string{"example.com/mono.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	if !findings[0].Dirty {
		t.Fatal("untracked runtime input below the repository root stamped clean - the false-clean portable row")
	}
	// Evidence is anchored at the tree: the member's input records
	// tree-relative (mod/data.txt) with no module base, so the portable
	// line and every revalidation resolve it at the tree root
	// (REQ-result-layers).
	if findings[0].TargetEvidence.ModuleBase != "" || findings[0].OracleEvidence[0].ModuleBase != "" {
		t.Fatalf("tree-anchored evidence carries a module base: %q/%q", findings[0].TargetEvidence.ModuleBase, findings[0].OracleEvidence[0].ModuleBase)
	}
	paths, err := runtimeinput.Paths(findings[0].OracleEvidence[0].RuntimeInputs, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, filepath.Join(root, "mod", "data.txt")) {
		t.Fatalf("the member's input is not recorded tree-relative: %v", paths)
	}
}

// ModuleBase is resolution metadata, never a measured pin: a record
// grown the field on its first post-upgrade measure keeps its
// dispositions (REQ-attest-survivor).
func TestModuleBaseIsNotAnAttestationPin(t *testing.T) {
	prior := storeFinding("p.A", nil)
	current := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.ModuleBase = "m"
		f.OracleEvidence[0].ModuleBase = "m"
	})
	if !sameAttestationPins(prior, current) {
		t.Fatal("module base shed the dispositions")
	}
}

// A hand-edited escaping module base is refused at parse: admitting it
// would draw the portable-containment line outside the tree
// (REQ-result-layers, REQ-result-export).
func TestParseRefusesEscapingModuleBase(t *testing.T) {
	for _, base := range []string{"..", "../x", "a/../b", "/abs", "a//b", "."} {
		f := storeFinding("p.A", func(f *Finding) { f.TargetEvidence.ModuleBase = base })
		data, err := Export([]Finding{f}, nil)
		if err == nil {
			_, err = ParseFindings(data)
		}
		if err == nil {
			t.Fatalf("module base %q accepted", base)
		}
	}
	clean := storeFinding("p.A", func(f *Finding) { f.TargetEvidence.ModuleBase = "m/n" })
	data, err := Export([]Finding{clean}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFindings(data); err != nil {
		t.Fatalf("clean module base refused: %v", err)
	}
}

// An identity outside the repository is not git's to vouch for: a clean
// tree whose oracle reads an external absolute input stamps clean and
// stays machine-local with the external input named in the portable
// line, never dirty-forever with a misleading reason
// (REQ-result-layers).
func TestExternalInputStampsCleanAndStaysMachineLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/ext\n\ngo 1.26.4\n",
		"p.go":      "package ext\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package ext\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadFile(\"/etc/hostname\"); err != nil {\n\t\tt.Skip(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
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
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")

	tr, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/ext.F", Oracle: []string{"example.com/ext.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	if findings[0].Dirty {
		t.Fatal("external absolute input stamped a clean tree dirty")
	}
	store, err := OpenStore(filepath.Join(root, ".gomutant", "findings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	// The full portable-line walk names the external input; the
	// observation bracket cannot cover it, so the unverifiable clause
	// holds alongside it and either may surface first.
	layer, reasons := store.LayerReasons(findings[0])
	named := false
	for _, r := range reasons {
		if r == "machine-local runtime input /etc/hostname" {
			named = true
		}
	}
	if layer != "local" || !named {
		t.Fatalf("external-input record layer = %s (%v), want machine-local with the input named", layer, reasons)
	}
}
