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

// Every oracle process runs with its own scratch TMPDIR, its contents
// swept - with permissions restored - as soon as the process ends, so a
// killed oracle's leaked temp directories are bounded to one run
// instead of accumulating tmpfs-backed RAM for the campaign
// (REQ-exec-oracle-scratch). The recordless manifest proves the
// admission covered the scratch; the empty temp root after the run
// proves the remove stage descends 0500 residue.
func TestOracleScratchContainsAndSweepsTempDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	hostScratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(hostScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", hostScratch)
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/scratch\n\ngo 1.26.4\n",
		"p.go":      "package scratch\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package scratch\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\td, err := os.MkdirTemp(\"\", \"layer-oracle-*\")\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tf := filepath.Join(d, \"data\")\n\tif err := os.WriteFile(f, []byte(\"x\"), 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif _, err := os.ReadFile(f); err != nil {\n\t\tt.Fatal(err)\n\t}\n\t// A killed oracle never runs cleanups; simulate the residue a\n\t// sweep must descend: a restrictive-mode directory left behind.\n\tif err := os.Chmod(d, 0o500); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
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
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/scratch.F", Oracle: []string{"example.com/scratch.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	// Sites that sweep only after finalization record content digests
	// of files the sweep deletes; the evidence union then reads such
	// inputs moved and replaces the manifest with the merge-failure
	// sentinel (demonstrated when every site regressed together; a
	// lone late site can be masked by the union's incomplete-evidence
	// exclusions). An external scratch read is inherently
	// bracket-uncoverable (its own unverifiable reason) - the sentinel,
	// not unverifiability itself, is the sweep-ordering regression.
	if strings.Contains(findings[0].TargetEvidence.RuntimeReason, "could not be merged") ||
		strings.Contains(findings[0].OracleEvidence[0].RuntimeReason, "could not be merged") {
		t.Fatalf("evidence union collapsed: target %q, oracle %q - an observation input moved between finalization and the union",
			findings[0].TargetEvidence.RuntimeReason, findings[0].OracleEvidence[0].RuntimeReason)
	}
	// Containment leaves no record: the minted root is declared as an
	// ephemeral temp root and the swept scratch reads beneath it are
	// absent at ingest, so they admit recordless instead of finalizing
	// as missing-path identities — a recorded gomutant-oracle-* identity
	// would mean a scratch read escaped the admission
	// (REQ-exec-oracle-scratch-declared).
	paths, err := runtimeinput.Paths(findings[0].OracleEvidence[0].RuntimeInputs, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if strings.Contains(p, "gomutant-oracle-") {
			t.Fatalf("scratch read recorded an identity despite the declared root: %q\noracle manifest: %s",
				p, findings[0].OracleEvidence[0].RuntimeInputs)
		}
	}
	if findings[0].OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("swept scratch left the evidence unverifiable: %q", findings[0].OracleEvidence[0].RuntimeReason)
	}
	// Sweep: nothing of the oracle scratch survives the run - the
	// 0500 directory included.
	leftovers, err := filepath.Glob(filepath.Join(hostScratch, "gomutant-oracle-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("oracle scratch leaked: %q", leftovers)
	}
	// The recorded evidence revalidates stably AFTER the sweep: the
	// sweep-before-finalization ordering finalizes the swept truth -
	// admitted scratch reads leave no record to move - where sweeping
	// after finalization would record content digests of files the
	// sweep deletes, evidence that reads moved forever
	// (REQ-exec-oracle-scratch-order).
	state, err := runtimeinput.CurrentEnvContext(context.Background(), findings[0].OracleEvidence[0].RuntimeInputs, dir, os.Environ())
	if err != nil || !state.OK {
		t.Fatalf("post-sweep revalidation = %+v, %v", state, err)
	}
	if state.Digest != findings[0].OracleEvidence[0].RuntimeDigest {
		t.Fatal("post-sweep revalidation moved - the sweep must precede observation finalization")
	}
}

// A temp-touching oracle - testing.TempDir, the enforced scratch
// namespace - finalizes a completed, verifiable observation: ingest
// declares the minted scratch root as an ephemeral temp root, so the
// root's stat (temp-tree creation machinery minting the per-test
// subtree) records nothing instead of sealing the evidence as an
// uncovered runtime input (REQ-exec-oracle-scratch-declared). Without
// the declaration every such oracle's findings are machine-local and
// its survivors unbucketed.
func TestTempTouchingOracleFinalizesVerifiable(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	hostScratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(hostScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", hostScratch)
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/scratch\n\ngo 1.26.4\n",
		"p.go":      "package scratch\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package scratch\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\td := t.TempDir()\n\tf := filepath.Join(d, \"data\")\n\tif err := os.WriteFile(f, []byte(\"x\"), 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif _, err := os.ReadFile(f); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
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
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/scratch.F", Oracle: []string{"example.com/scratch.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	if findings[0].OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("temp-touching oracle evidence is unverifiable: %q\nmanifest: %s",
			findings[0].OracleEvidence[0].RuntimeReason, findings[0].OracleEvidence[0].RuntimeInputs)
	}
	if findings[0].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("target evidence is unverifiable: %q", findings[0].TargetEvidence.RuntimeReason)
	}
	// The evidence is reuse-ready: it revalidates against the current
	// tree after the scratch sweep.
	state, err := runtimeinput.CurrentEnvContext(context.Background(), findings[0].OracleEvidence[0].RuntimeInputs, dir, os.Environ())
	if err != nil || !state.OK || state.Unverifiable {
		t.Fatalf("post-run revalidation = %+v, %v", state, err)
	}
	if state.Digest != findings[0].OracleEvidence[0].RuntimeDigest {
		t.Fatal("post-run revalidation moved")
	}
}
