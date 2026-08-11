package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// A test-only delta's residue row names the oracle closure it left
// stale and the re-measure move - the run face carries the signpost,
// not only the library (REQ-target-changed).
func TestRunCommandChangedTestResidueCarriesOracleClosureSignpost(t *testing.T) {
	// isolatedFixture is already a committed git repo; the uncommitted
	// test edit below is the changed surface.
	fixture := isolatedFixture(t)
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	stale := gomutant.Finding{Symbol: "example.com/fixture/lib.Weak", BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/fixture/lib.Weak"),
		OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/fixture/lib.TestGone")}}
	if err := gomutant.UpdateDocument(findingsAt(fixture, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
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

	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixture, changed: "HEAD", findingsFile: defaultFindings, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "oracle closure of 1 stale finding(s) - re-measure by symbol: example.com/fixture/lib.Weak") {
		t.Fatalf("changed-mode residue missing the signpost:\n%s", output.String())
	}
}
