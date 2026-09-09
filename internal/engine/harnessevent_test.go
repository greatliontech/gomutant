package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A baseline is classified a build failure by the harness's own event,
// never by output text: a passing baseline whose test prints the
// "[build failed]" line passes, while a package that cannot compile
// still refuses with the compile diagnostic.
func TestProbeBaselineIgnoresForgedBuildFailureText(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	// A -coverpkg pattern nothing matches makes the go tool print a
	// warning on stderr ahead of every event: the classifier reads the
	// event stream alone, so the line decides nothing and the
	// diagnostic still carries it.
	t.Setenv("GOFLAGS", "-coverpkg=example.com/nomatch/...")
	ran, passed, _, err := TestProbe(context.Background(), "testdata/fixturemod", "example.com/fixture/forgery", "^TestForgedBaseline$", 60*time.Second, nil, OracleBounds{})
	if err != nil || ran != 1 || !passed {
		t.Fatalf("forged baseline: ran=%d passed=%v err=%v; want a passing baseline", ran, passed, err)
	}
	_, _, _, err = TestProbe(context.Background(), "testdata/fixturemod", "example.com/fixture/broken", "^Test", 60*time.Second, nil, OracleBounds{})
	var build *BaselineBuildError
	if !errors.As(err, &build) {
		t.Fatalf("broken package baseline = %v, want the build-failure refusal", err)
	}
	for _, want := range []string{"undefined: undefinedIdentifier", "warning: no packages being tested"} {
		if !strings.Contains(build.Diagnostic, want) {
			t.Fatalf("build-failure diagnostic %q lacks %q", build.Diagnostic, want)
		}
	}
}

// A baseline is classified by the event stream alone: a go-tool line on
// stderr ahead of the events, or a test printing the build-failure text
// on stdout, never decides, and a real build-fail event refuses with the
// compiler's diagnostic drawn from both streams.
func TestHarnessBuildFailureReadsTheEventStream(t *testing.T) {
	event := "{\"Action\":\"build-fail\",\"FailedBuild\":\"example.com/p\"}\n"
	if diagnostic, rejected := harnessBuildFailure([]byte(event), []byte("go: downloading example.com/dep v1.0.0\n# example.com/p\n./p.go:3:1: undefined: x\n")); !rejected || !strings.Contains(diagnostic, "undefined: x") {
		t.Fatalf("a build-fail event beside a go-tool stderr line = rejected %v, diagnostic %q", rejected, diagnostic)
	}
	forged := "{\"Action\":\"output\",\"Package\":\"example.com/p\",\"Output\":\"captured tool output: FAIL\\texample.com/other [build failed]\\n\"}\n"
	if _, rejected := harnessBuildFailure([]byte(forged), nil); rejected {
		t.Fatal("output text naming a build failure classified the baseline")
	}
	if _, rejected := harnessBuildFailure(nil, []byte("go: downloading example.com/dep v1.0.0\n")); rejected {
		t.Fatal("a go-tool stderr line alone classified the baseline")
	}
}
