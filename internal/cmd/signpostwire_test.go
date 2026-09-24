package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// A test-only delta's residue row names the oracle closure it left
// stale and the re-measure move - the run face carries the signpost,
// not only the library (REQ-target-changed). The stretches the run
// names on the way are the vocabulary's, in order — the preparation,
// the load's event, the selection before the selection runs, the
// signpost's pass under the inspection lead — the same words the
// structured face's heartbeat reads (REQ-exec-run-status).
func TestRunCommandChangedTestResidueCarriesOracleClosureSignpost(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	// isolatedFixture is already a committed git repo; the uncommitted
	// test edit below is the changed surface.
	fixture := isolatedFixture(t)
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: symbol}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
	}
	stale := gomutant.Finding{Symbol: "example.com/fixture/lib.Weak", BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/fixture/lib.Weak"),
		OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/fixture/lib.TestGone")}}
	if err := gomutant.UpdateDocument(context.Background(), gomutant.FindingsPathAt(fixture, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{stale}, nil
	}); err != nil {
		t.Fatal(err)
	}

	libTest := filepath.Join(fixture, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libTest, append(src, []byte("\nfunc TestClosureAnchor(t *testing.T) {}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	labels := observeStretches(t)
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixture, changed: "HEAD", findingsFile: defaultFindings, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "oracle closure of 1 stale finding(s) - re-measure by symbol: example.com/fixture/lib.Weak") {
		t.Fatalf("changed-mode residue missing the signpost:\n%s", output.String())
	}
	wantStretchesInOrder(t, labels(), []string{gomutant.StretchPreparation, gomutant.StretchPreparing(gomutant.PreparationEvent{Stage: gomutant.PreparationLoading}), gomutant.StretchSelecting, gomutant.StretchInspecting("closure signpost listing the test closure of 1 changed package(s)"), gomutant.StretchInspecting("closure signpost over 1 prior record(s) the changed tests reach (1 subject(s) in 1 package(s))")})
}
