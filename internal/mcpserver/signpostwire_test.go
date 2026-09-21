package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The MCP run response's changed-mode residue carries the oracle
// closure signpost like the CLI's (REQ-target-changed, spec mcp.md's
// same-shell rule), and the signpost's pass names the heartbeat's
// stretch under the inspection lead — the CLI's cadence's words
// (REQ-exec-run-status).
func TestToolRunChangedTestResidueCarriesOracleClosureSignpost(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
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
		return gomutant.SubjectEvidence{Symbol: symbol, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: symbol}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
	}
	stale := gomutant.Finding{Symbol: "example.com/fixture/lib.Weak", BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/fixture/lib.Weak"),
		OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/fixture/lib.TestGone")}}
	if err := gomutant.UpdateDocument(context.Background(), filepath.Join(s.dir, gomutant.DefaultFindingsPath), func([]gomutant.Finding) ([]gomutant.Finding, error) {
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

	labels := observeStretches(t)
	_, out, err := s.toolRun(context.Background(), nil, runIn{Changed: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	wantStretchesInOrder(t, labels(), []string{gomutant.StretchSelecting, gomutant.StretchInspecting("closure signpost over 1 prior record(s)")})
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
