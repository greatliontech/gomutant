package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// An oracle's reads under the toolchain and under the tool's own
// bookkeeping surfaces are not runtime inputs of the measurement: the
// producer facade resolves the toolchain and cache roots from the
// process environment gomutant hands it, and gomutant excludes its
// bookkeeping directories from the ingest, so such reads record no
// identity and seal nothing — the record stays portable
// (REQ-result-layers; gofresh REQ-inputs-guard-covered).
func TestOracleToolchainAndBookkeepingReadsAdmitRecordless(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one campaign")
	}
	if runtime.GOOS == "windows" {
		t.Skip("path shapes")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	// An external read under a declared bracket path proves observation
	// was live: it must be recorded (and it is the record's one
	// machine-local input).
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":         "module example.com/rootsmod\n\ngo 1.26\n",
		".gomutant/note": "bookkeeping\n",
		"a/a.go":         "package a\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
		"a/a_test.go": "package a\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"runtime\"\n\t\"testing\"\n)\n\n" +
			"func TestAdd(t *testing.T) {\n\tif _, err := os.ReadFile(filepath.Join(runtime.GOROOT(), \"VERSION\")); err != nil {\n\t\tt.Fatal(err)\n\t}\n" +
			"\tif _, err := os.ReadFile(filepath.Join(\"..\", \".gomutant\", \"note\")); err != nil {\n\t\tt.Fatal(err)\n\t}\n" +
			"\tif _, err := os.ReadFile(" + strconv.Quote(external) + "); err != nil {\n\t\tt.Fatal(err)\n\t}\n" +
			"\tif Add(1, 2) != 3 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	own := RunOwnWrites(filepath.Join(dir, ".gomutant", "findings.json"))
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/rootsmod/a.Add"}}, Options{Budget: 1, Jobs: 1, OwnWrites: own, BracketPaths: []string{external}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Skipped != "" {
		t.Fatalf("findings = %+v", findings)
	}
	sawExternal := false
	for _, ev := range append([]SubjectEvidence{findings[0].TargetEvidence}, findings[0].OracleEvidence...) {
		if ev.RuntimeUnverifiable {
			t.Fatalf("%s: unverifiable: %s", ev.Symbol, ev.RuntimeReason)
		}
		if ev.RuntimeInputs == "" {
			continue
		}
		paths, err := runtimeinput.Paths(ev.RuntimeInputs, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			if p == external {
				sawExternal = true
			}
			if strings.HasPrefix(p, runtime.GOROOT()) || strings.Contains(p, string(filepath.Separator)+".gomutant"+string(filepath.Separator)) {
				t.Fatalf("%s recorded %s as a runtime input; the toolchain and the bookkeeping surfaces are not the oracle's inputs", ev.Symbol, p)
			}
		}
	}
	if !sawExternal {
		t.Fatal("the external read was not recorded: observation was not live, so the admissions above prove nothing")
	}
	// The portable line names the external read and nothing under the
	// toolchain or the bookkeeping surfaces (the fixture has no git
	// provenance, which is its own clause).
	for _, clause := range CommittableReasons(findings[0], dir, nil) {
		if strings.HasPrefix(clause, "machine-local runtime input ") && !strings.HasSuffix(clause, external) {
			t.Fatalf("a toolchain or bookkeeping read reached the portable line: %s", clause)
		}
	}
}
