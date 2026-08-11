package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The MCP run response's changed-mode residue carries the oracle
// closure signpost like the CLI's (REQ-target-changed, spec mcp.md's
// same-shell rule).
func TestToolRunChangedTestResidueCarriesOracleClosureSignpost(t *testing.T) {
	s := serverAt(t)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.dir
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

	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	stale := gomutant.Finding{Symbol: "example.com/fixture/lib.Weak", BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/fixture/lib.Weak"),
		OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/fixture/lib.TestGone")}}
	if err := gomutant.UpdateDocument(filepath.Join(s.dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{stale}, nil
	}); err != nil {
		t.Fatal(err)
	}

	libTest := filepath.Join(s.dir, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libTest, append(src, []byte("\nfunc TestClosureAnchor(t *testing.T) {}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	_, out, err := s.toolRun(context.Background(), nil, runIn{Changed: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range out.Residue {
		if strings.Contains(r.Reason, "oracle closure of 1 stale finding(s) - re-measure by symbol: example.com/fixture/lib.Weak") {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed-mode residue missing the signpost: %+v", out.Residue)
	}
}
