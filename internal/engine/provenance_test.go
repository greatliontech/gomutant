package engine

import (
	"context"
	"fmt"
	"go/version"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
)

// Every load refuses a frontend older than the toolchain serving it,
// before any package loads, sampling in the TARGET directory under
// the SELECTION-APPLIED environment — the declared toolchain
// directive is what gets witnessed (REQ-exec-provenance).
func TestLoadRefusesToolchainSkewUnderSelectionEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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

// The production ladder samples through gofresh's memoized sampler
// under the tree's runner: the sample is the trimmed GOVERSION (the
// dir half — the sample runs in the target module's directory — is
// gofresh's contract, pinned there by gotool's
// TestSampleGoVersionRunsInTheModuleDirectory; the env half is
// witnessed through an undownloadable directive, refused in the
// provenance composite's words over go's own cause), and one ladder
// asks the sampler ONCE: the provenance check hands the build-events
// floor the sample it judged (REQ-exec-provenance).
func TestToolchainLadderSamplesOnce(t *testing.T) {
	env := GoEnv("testdata/fixturemod")
	sampled, err := toolchainGuard(context.Background(), "testdata/fixturemod", env)
	if err != nil || !strings.HasPrefix(sampled, "go") || strings.ContainsAny(sampled, " \n") {
		t.Fatalf("sample = %q, %v; want the trimmed GOVERSION", sampled, err)
	}
	undownloadable := gotool.SetEnv(env, "GOTOOLCHAIN", "go0.0.0-nosuch")
	if _, err := toolchainGuard(context.Background(), "testdata/fixturemod", undownloadable); err == nil || !strings.Contains(err.Error(), "ambient toolchain unidentifiable") || !strings.Contains(err.Error(), "go0.0.0-nosuch") {
		t.Fatalf("an undownloadable directive = %v; want the composite's unidentifiable refusal naming go's cause", err)
	}
	spawns := 0
	prior := newToolchainSampler
	newToolchainSampler = func() gofresh.ToolchainSampler {
		return &gotool.Sampler{Runner: gotool.Runner{Prepare: func(*exec.Cmd) { spawns++ }}}
	}
	defer func() { newToolchainSampler = prior }()
	if _, err := toolchainGuard(context.Background(), "testdata/fixturemod", env); err != nil {
		t.Fatal(err)
	}
	if spawns != 1 {
		t.Fatalf("one ladder spawned %d samples, want 1 — the floor reads the sample the check judged", spawns)
	}
	// The injected sampler is unmemoized, so its asks are the ladder's
	// reads: one.
	asks := 0
	restore := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		asks++
		return runtime.Version(), nil
	})
	defer restore()
	if _, err := toolchainGuard(context.Background(), "testdata/fixturemod", env); err != nil {
		t.Fatal(err)
	}
	if asks != 1 {
		t.Fatalf("one ladder asked the injected sampler %d times, want 1", asks)
	}
}

// The attestation path's provenance check carries the load's build-events
// floor: a sampled toolchain below go1.24 refuses there as it refuses at
// the load, so a verb that checks before its write admits nothing the
// load would refuse (REQ-exec-provenance).
func TestCheckToolchainProvenanceCarriesTheBuildEventsFloor(t *testing.T) {
	restore := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		return "go1.23.4", nil
	})
	defer restore()
	err := CheckToolchainProvenance(context.Background(), "testdata/fixturemod", Selection{})
	if err == nil || !strings.Contains(err.Error(), "below go1.24") {
		t.Fatalf("below-floor sample through the provenance check = %v, want the build-events floor refusal", err)
	}
}

// The environment arm of the load's ladder: GODEBUG's
// gotestjsonbuildtext=1 silences the build-fail event the classifier
// reads, so the load and the attestation path's standalone check
// refuse it alike, judged as the go command judges its own setting —
// the GODEBUG entry's last pair.
func TestLoadRefusesASilencedHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	// The arm's inputs alone decide it, so it refuses before the
	// toolchain is sampled — on the load and on the standalone check.
	sampled := false
	restore := SwapGoVersionSamplerForTest(func(context.Context, string, []string) (string, error) {
		sampled = true
		return runtime.Version(), nil
	})
	defer restore()
	t.Setenv("GODEBUG", "gotestjsonbuildtext=1")
	_, err := loadContext(context.Background(), "testdata/fixturemod", Selection{}, true)
	if err == nil || !strings.Contains(err.Error(), "gotestjsonbuildtext=1") {
		t.Fatalf("load under a silenced harness = %v, want the refusal naming the setting", err)
	}
	if err := CheckToolchainProvenance(context.Background(), "testdata/fixturemod", Selection{}); err == nil || !strings.Contains(err.Error(), "gotestjsonbuildtext=1") {
		t.Fatalf("standalone check under a silenced harness = %v, want the refusal naming the setting", err)
	}
	if sampled {
		t.Fatal("the environment arm sampled the toolchain before refusing")
	}
	t.Setenv("GODEBUG", "gotestjsonbuildtext=1,gotestjsonbuildtext=0")
	if err := CheckToolchainProvenance(context.Background(), "testdata/fixturemod", Selection{}); err != nil {
		t.Fatalf("a later pair re-enabling the events refused: %v", err)
	}
}

func TestHarnessEventsSilencedReadsTheEffectiveSetting(t *testing.T) {
	for _, row := range []struct {
		env      []string
		silenced bool
	}{
		{nil, false},
		{[]string{"GODEBUG=gotestjsonbuildtext=0"}, false},
		{[]string{"GODEBUG=gotestjsonbuildtext=1"}, true},
		{[]string{"GODEBUG=http2client=0,gotestjsonbuildtext=1"}, true},
		{[]string{"GODEBUG=gotestjsonbuildtext=1,gotestjsonbuildtext=0"}, false},
		{[]string{"GODEBUG=gotestjsonbuildtext=0,gotestjsonbuildtext=1"}, true},
		// A duplicated key never reaches the arm: the ladder's ambient
		// refusal precedes it (TestAmbientEnvironmentRefusals) and the
		// policy's setter composes each key once.
		{[]string{"GODEBUGX=gotestjsonbuildtext=1"}, false},
		// A bisect suffix is stripped before the tool reads the value;
		// the key is case-folded only where the spawn folds it.
		{[]string{"GODEBUG=gotestjsonbuildtext=1#x"}, true},
		{[]string{"GODEBUG=gotestjsonbuildtext=1#v1,http2client=0"}, true},
		{[]string{"GODEBUG=gotestjsonbuildtext=01"}, false},
		{[]string{"GODEBUG=gotestjsonbuildtext=1x"}, false},
		{[]string{"godebug=gotestjsonbuildtext=1"}, runtime.GOOS == "windows"},
	} {
		if got := harnessEventsSilenced(row.env) != nil; got != row.silenced {
			t.Fatalf("%q silenced = %v, want %v", row.env, got, row.silenced)
		}
	}
}

// TestAmbientEnvironmentRefusals pins the environment arm's read of
// the OS environment itself: a live package driver is refused by name
// (off and empty are not a driver), an environment the exec key=value
// form cannot express — a duplicate key, a malformed entry, a NUL
// byte — is refused in the operator's words, and the arm precedes the
// composed environment's own on the preparation entry and heads the
// load's ladder the pre-write check reads (REQ-exec-preparation,
// REQ-exec-provenance).
func TestAmbientEnvironmentRefusals(t *testing.T) {
	for _, row := range []struct {
		env     []string
		refused string
	}{
		{nil, ""},
		{[]string{"GOPACKAGESDRIVER=off", "A=1"}, ""},
		{[]string{"GOPACKAGESDRIVER="}, ""},
		{[]string{"GOPACKAGESDRIVER=/usr/bin/gopackagesdriver"}, `GOPACKAGESDRIVER="/usr/bin/gopackagesdriver" is unsupported`},
		{[]string{"A=1", "B=2", "A=3"}, `duplicate key "A"`},
		{[]string{"A=1", "NOEQUALS"}, "malformed"},
		{[]string{"A=1\x00b"}, "NUL"},
	} {
		err := ambientEnvironmentRefused(row.env)
		if (err != nil) != (row.refused != "") || err != nil && !strings.Contains(err.Error(), row.refused) {
			t.Fatalf("%q refused = %v, want %q", row.env, err, row.refused)
		}
	}
	t.Setenv("GOPACKAGESDRIVER", "/usr/bin/gopackagesdriver")
	if err := CheckHarnessEnvironment("testdata/fixturemod", Selection{}); err == nil || !strings.Contains(err.Error(), "GOPACKAGESDRIVER") {
		t.Fatalf("the preparation entry under an ambient driver = %v, want the driver refused by name", err)
	}
	if err := CheckToolchainProvenance(context.Background(), "testdata/fixturemod", Selection{}); err == nil || !strings.Contains(err.Error(), "GOPACKAGESDRIVER") {
		t.Fatalf("the pre-write check under an ambient driver = %v, want the ladder's head refusing", err)
	}
	// The OS environment's own refusals precede the composed
	// environment's: under both faults the driver is named, never the
	// silenced harness.
	t.Setenv("GODEBUG", "gotestjsonbuildtext=1")
	if err := CheckHarnessEnvironment("testdata/fixturemod", Selection{}); err == nil || !strings.Contains(err.Error(), "GOPACKAGESDRIVER") || strings.Contains(err.Error(), "gotestjsonbuildtext") {
		t.Fatalf("both faults = %v, want the ambient half's refusal first", err)
	}
}
