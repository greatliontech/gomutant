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

// Every oracle process runs with its own scratch TMPDIR, swept - with
// permissions restored - as soon as the process ends, so a killed
// oracle's leaked temp directories are bounded to one run instead of
// accumulating tmpfs-backed RAM for the campaign (REQ-exec-oracle-scratch).
// The manifest's recorded identity proves containment; the empty
// scratch root after the run proves the sweep descends 0500 residue.
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
		"go.mod": "module example.com/scratch\n\ngo 1.26.4\n",
		"p.go":   "package scratch\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
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
	// Containment: the oracle's temp writes landed under a
	// campaign-owned scratch directory beneath the operator's temp
	// root, visible in the recorded runtime-input identity.
	paths, err := runtimeinput.Paths(findings[0].OracleEvidence[0].RuntimeInputs, dir)
	if err != nil {
		t.Fatal(err)
	}
	contained := false
	for _, p := range paths {
		if strings.HasPrefix(p, hostScratch+string(filepath.Separator)) && strings.Contains(p, "gomutant-oracle-") {
			contained = true
		}
	}
	if !contained {
		t.Fatalf("no recorded identity under the oracle scratch root: %q\noracle manifest: %s\ntarget manifest: %s\nunverifiable: %v %q",
			paths, findings[0].OracleEvidence[0].RuntimeInputs, findings[0].TargetEvidence.RuntimeInputs,
			findings[0].OracleEvidence[0].RuntimeUnverifiable, findings[0].OracleEvidence[0].RuntimeReason)
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
	// sweep-before-finalization ordering makes scratch reads finalize
	// as missing-path identities - sweeping after finalization would
	// record content digests of deleted files and the evidence would
	// read moved forever (REQ-exec-oracle-scratch).
	state, err := runtimeinput.CurrentEnvContext(context.Background(), findings[0].OracleEvidence[0].RuntimeInputs, dir, os.Environ())
	if err != nil || !state.OK {
		t.Fatalf("post-sweep revalidation = %+v, %v", state, err)
	}
	if state.Digest != findings[0].OracleEvidence[0].RuntimeDigest {
		t.Fatal("post-sweep revalidation moved - the sweep must precede observation finalization")
	}
}
