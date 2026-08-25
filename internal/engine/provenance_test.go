package engine

import (
	"context"
	"fmt"
	"go/version"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Every load refuses a frontend older than the toolchain serving it,
// before any package loads, sampling in the TARGET directory under
// the SELECTION-APPLIED environment — the declared toolchain
// directive is what gets witnessed (REQ-exec-provenance).
func TestLoadRefusesToolchainSkewUnderSelectionEnv(t *testing.T) {
	var sampledDir string
	var sampledEnv []string
	restore := SwapGoVersionSamplerForTest(func(_ context.Context, dir string, env []string) (string, error) {
		sampledDir, sampledEnv = dir, append([]string(nil), env...)
		return "go99.1.0", nil // a series this binary's frontend predates
	})
	defer restore()

	_, err := loadContext(context.Background(), "testdata/fixturemod", Selection{Toolchain: "go99.1.0"}, true)
	if err == nil || !strings.Contains(err.Error(), "toolchain provenance") {
		t.Fatalf("load under skew = %v, want the provenance refusal", err)
	}
	if sampledDir != "testdata/fixturemod" {
		t.Fatalf("sampled in %q, want the target directory", sampledDir)
	}
	// The sampler saw the SELECTION-APPLIED environment: the declared
	// toolchain directive present, so the witnessed version is the one
	// the run would actually use.
	found := false
	for _, kv := range sampledEnv {
		if kv == "GOTOOLCHAIN=go99.1.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sampler env lacks the declared toolchain directive: %v", sampledEnv)
	}
	// The WITHIN-MAJOR contract, both directions (the field class and
	// the workflow the spec leans on): a same-major sample one series
	// NEWER than this binary's refuses; one series OLDER passes (the
	// declared-toolchain workflow).
	series := version.Lang(runtime.Version())
	majorStr, minorStr, ok := strings.Cut(strings.TrimPrefix(series, "go"), ".")
	if !ok {
		t.Fatalf("own series %q unparseable", series)
	}
	minor, err2 := strconv.Atoi(minorStr)
	if err2 != nil {
		t.Fatalf("own series %q unparseable", series)
	}
	newer := fmt.Sprintf("go%s.%d.0", majorStr, minor+1)
	older := fmt.Sprintf("go%s.%d.0", majorStr, minor-1)
	restoreNewer := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return newer, nil
	})
	if _, err := loadContext(context.Background(), "testdata/fixturemod", Selection{}, true); err == nil || !strings.Contains(err.Error(), "predates") {
		restoreNewer()
		t.Fatalf("same-major newer ambient = %v, want the frontend-predates refusal", err)
	}
	restoreNewer()
	if majorStr == "1" && minor-1 < 24 {
		t.Logf("older-direction leg skipped: go1.%d is below the build-events floor — the floor, not provenance, would refuse", minor-1)
	} else {
		restoreOlder := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
			return older, nil
		})
		if _, err := loadContext(context.Background(), "testdata/fixturemod", Selection{}, true); err != nil {
			restoreOlder()
			t.Fatalf("same-major older ambient refused: %v — the declared-toolchain workflow direction must pass", err)
		}
		restoreOlder()
	}

	// The floor judges the SAMPLED toolchain (the consolidation's one
	// exec serves both checks): a below-floor sample that PASSES
	// provenance (older-within-major is the supported workflow) must
	// still refuse on the go1.24 build-events floor — the only thing
	// between that run and an event-less stream scoring uncompilable
	// mutants as kills.
	restoreFloor := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return "go1.23.4", nil
	})
	if _, err := loadContext(context.Background(), "testdata/fixturemod", Selection{}, true); err == nil || !strings.Contains(err.Error(), "below go1.24") {
		restoreFloor()
		t.Fatalf("below-floor sample = %v, want the build-events floor refusal", err)
	}
	restoreFloor()

	// An unidentifiable sample refuses too — unidentifiable is not
	// agreement.
	restore2 := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return "devel +abc", nil
	})
	defer restore2()
	if _, err := loadContext(context.Background(), "testdata/fixturemod", Selection{}, true); err == nil || !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("unidentifiable ambient = %v, want the unidentifiable refusal", err)
	}
}

// The standalone check (attest's pre-write guard) shares the load
// guard's sampling exactly.
func TestCheckToolchainProvenanceSharesTheGuard(t *testing.T) {
	restore := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return "go99.1.0", nil
	})
	defer restore()
	if err := CheckToolchainProvenance(context.Background(), "testdata/fixturemod", Selection{}); err == nil || !strings.Contains(err.Error(), "toolchain provenance") {
		t.Fatalf("standalone check under skew = %v, want the provenance refusal", err)
	}
}

// The production sampler's construction wires the target dir and the
// selection-applied env onto the exec — the wiring a whole-function
// seam can never exercise (a healthy host's `go env GOVERSION` is
// invariant under both).
func TestGoVersionCmdWiresDirAndEnv(t *testing.T) {
	env := []string{"GOTOOLCHAIN=go1.26.5", "HOME=/x"}
	cmd := goVersionCmd(context.Background(), "some/target", env)
	if cmd.Dir != "some/target" {
		t.Fatalf("cmd.Dir = %q, want the target directory", cmd.Dir)
	}
	found := false
	for _, kv := range cmd.Env {
		if kv == "GOTOOLCHAIN=go1.26.5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cmd.Env lacks the declared directive: %v", cmd.Env)
	}
	if got := strings.Join(cmd.Args, " "); got != "go env GOVERSION" {
		t.Fatalf("cmd args = %q", got)
	}
}
