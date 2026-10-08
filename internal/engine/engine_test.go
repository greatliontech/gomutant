package engine

import (
	"runtime"
	"testing"
)

// The build-failure classification's toolchain floor: versions below go1.24
// lack the harness's build-fail events, so loading refuses them rather than
// letting uncompilable mutants fall through to the differential probe and
// score as kills. The floor reads the sample the ladder hands it — the
// trimmed GOVERSION — through gotool's canonical series: a vendor flavor,
// an experiment suffix, a release candidate and the development-build
// spelling read as their series; the legacy "devel" spelling the grammar
// refuses is modern by construction and passes (REQ-exec-provenance).
func TestBuildEventsFloorReadsTheToolchainSeries(t *testing.T) {
	cases := []struct {
		version string
		refused bool
	}{
		{"go1.23.4", true},
		{"go1.24.0", false},
		{"go1.26.5-X:nodwarf5", false},
		{"go1.27.1-dst.13", false},
		{"go1.28rc1", false},
		{"go1.28-devel_abc123", false},
		{"go1.23rc1", true},
		{"go1.24rc1", false},
		{"devel +abc123", false},
	}
	for _, c := range cases {
		err := toolchainSupportsBuildEvents(c.version)
		if (err != nil) != c.refused {
			t.Fatalf("toolchainSupportsBuildEvents(%q) = %v; want refused=%v", c.version, err, c.refused)
		}
	}
	if err := toolchainSupportsBuildEvents(runtime.Version()); err != nil {
		t.Fatalf("current toolchain refused: %v", err)
	}
	if err := toolchainSupportsBuildEvents("go1.23.4"); err == nil {
		t.Fatal("below-floor toolchain accepted")
	}
	if !belowBuildEventFloor(1, 23) || belowBuildEventFloor(1, 24) || belowBuildEventFloor(2, 0) {
		t.Fatal("build-event floor is not exactly go1.24")
	}
}
