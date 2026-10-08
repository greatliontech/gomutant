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
	"github.com/greatliontech/gomutant/internal/engine"
)

// Check the per-process state before an across-process union can discard
// disagreeing environment values. This keeps sweep order and root coverage
// observable even when each process legitimately has a different TMPDIR.
func assertScratchProcessesFinalize(t *testing.T, tree *Tree, dir string) {
	t.Helper()
	mutants, err := tree.eng.Mutants("example.com/scratch.F", 1)
	if err != nil || len(mutants) == 0 {
		t.Fatalf("mutants: %v %v", mutants, err)
	}
	for _, baseline := range []bool{true, false} {
		var env []string
		restore := engine.ObserveGoCommandsForTest(func(cmd *exec.Cmd) {
			if len(cmd.Args) > 1 && cmd.Args[1] == "test" {
				env = slices.Clone(cmd.Env)
			}
		})
		var observation runtimeinput.Observation
		if baseline {
			var ran int
			var passed bool
			ran, passed, _, _, observation, err = engine.TestProbeObservedEnv(context.Background(), dir, "example.com/scratch", "^TestF$", 2*time.Minute, nil, dir, dir, nil, nil, tree.eng.GoEnv(), engine.OracleBounds{})
			if err == nil && (ran != 1 || !passed) {
				t.Fatalf("baseline ran=%d passed=%v", ran, passed)
			}
		} else {
			var incomplete string
			_, _, _, observation, incomplete, _, err = engine.RunMutantObserved(context.Background(), dir, mutants[0], []string{"example.com/scratch"}, "^TestF$", 2*time.Minute, nil, dir, dir, nil, nil, engine.OracleBounds{})
			if incomplete != "" {
				t.Fatalf("mutant capture incomplete: %s", incomplete)
			}
		}
		restore()
		if err != nil || !observation.OK || observation.Unverifiable || len(env) == 0 {
			t.Fatalf("baseline=%v observation=%+v err=%v", baseline, observation, err)
		}
		paths, err := runtimeinput.Paths(observation.Manifest, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.Contains(path, "gomutant-oracle-") {
				t.Fatalf("scratch identity escaped its root declaration: %s", path)
			}
		}
		state, err := runtimeinput.Current(context.Background(), observation.Manifest, dir, env)
		if err != nil || state.Unverifiable || state.Digest != observation.Digest {
			t.Fatalf("baseline=%v post-sweep state=%+v observation=%+v err=%v", baseline, state, observation, err)
		}
	}
}

// Every oracle process runs with its own scratch TMPDIR, its contents
// swept - with permissions restored - as soon as the process ends, so a
// killed oracle's leaked temp directories are bounded to one run
// instead of accumulating tmpfs-backed RAM for the campaign
// (REQ-exec-oracle-scratch). Per-process path guards prove the
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
	assertScratchProcessesFinalize(t, tree, dir)
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/scratch.F", Oracle: []string{"example.com/scratch.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	// Root coverage and sweep order were checked before union. The actual
	// TMPDIR reads differ across processes and must not become a stable value.
	if !findings[0].TargetEvidence.RuntimeUnverifiable || !findings[0].OracleEvidence[0].RuntimeUnverifiable {
		t.Fatal("different delivered scratch names acquired reusable union evidence")
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
	// Sweep: nothing of the oracle scratch survives the run - the
	// 0500 directory included.
	leftovers, err := filepath.Glob(filepath.Join(hostScratch, "gomutant-oracle-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("oracle scratch leaked: %q", leftovers)
	}
	// The union's explicit refusal survives revalidation; per-process sweep
	// ordering was checked against each process's actual environment above.
	state, err := runtimeinput.Current(context.Background(), findings[0].OracleEvidence[0].RuntimeInputs, dir, os.Environ())
	if err != nil || !state.OK || !state.Unverifiable {
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
// uncovered runtime input (REQ-exec-oracle-scratch-declared). This checks
// each process's state under its delivered environment; differing TMPDIR
// values can independently prevent an across-process reusable union.
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
	assertScratchProcessesFinalize(t, tree, dir)
}
