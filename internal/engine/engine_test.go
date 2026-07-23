package engine

import (
	"context"
	"testing"
)

// The build-failure classification's toolchain floor: versions below go1.24
// lack the harness's build-fail events, so loading refuses them rather than
// letting uncompilable mutants fall through to the differential probe and
// score as kills; devel strings are modern by construction and pass
// (candidate evidence term's harness-witness sentence).
func TestParseGoVersionFloorsBuildEventToolchains(t *testing.T) {
	cases := []struct {
		version      string
		major, minor int
		ok           bool
	}{
		{"go version go1.23.4 linux/amd64", 1, 23, true},
		{"go version go1.24.0 linux/amd64", 1, 24, true},
		{"go version go1.26.5-X:nodwarf5 linux/amd64", 1, 26, true},
		{"go version devel +abc123 linux/amd64", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseGoVersion(c.version)
		if major != c.major || minor != c.minor || ok != c.ok {
			t.Fatalf("parseGoVersion(%q) = %d, %d, %v; want %d, %d, %v", c.version, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
	if err := toolchainSupportsBuildEvents(context.Background(), "testdata/fixturemod"); err != nil {
		t.Fatalf("current toolchain refused: %v", err)
	}
	if !belowBuildEventFloor(1, 23) || belowBuildEventFloor(1, 24) || belowBuildEventFloor(2, 0) {
		t.Fatal("build-event floor is not exactly go1.24")
	}
}
