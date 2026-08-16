package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The server's vouch set reaches every tree it serves: the cache-miss
// load installs it, and the cached hit returns a tree installed at its
// own load - concurrent tool calls always judge under the server's set.
func TestServerInstallsVouchesOnLoadedTrees(t *testing.T) {
	s := serverAt(t)
	want := []string{"a.example/dep.Var"}
	s.vouches = append([]string(nil), want...)
	tree, err := s.loadTreeContext(context.Background(), gomutant.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.DynamicStateVouches(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cache-miss tree vouches = %v, want %v", got, want)
	}
	cached, err := s.loadTreeContext(context.Background(), gomutant.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cached.DynamicStateVouches(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cached tree vouches = %v, want %v", got, want)
	}
	if New(s.dir, WithDynamicStateVouches(want...)).vouches[0] != want[0] {
		t.Fatal("construction option did not install the set")
	}
}

// An explicit probe ceiling refuses while a run is in flight - the
// campaign owns the process ceiling - and the probe path restores the
// exact prior state, the installed flag included
// (REQ-exec-oracle-memory).
func TestEphemeralCeilingRefusesDuringRunAndRestores(t *testing.T) {
	s := serverAt(t)
	// The campaign's admission path is the width claim - the same guard
	// the probe override checks, closing the old check-then-install
	// window (REQ-exec-oracle-parallelism, REQ-exec-oracle-memory).
	if err := s.claimRunWidth(4); err != nil {
		t.Fatal(err)
	}
	mib := int64(256)
	if _, _, err := s.toolEphemeral(context.Background(), nil, ephemeralIn{OracleMemoryMiB: &mib}); err == nil || !strings.Contains(err.Error(), "owns the process's oracle width and memory ceiling") {
		t.Fatalf("in-flight explicit ceiling accepted: %v", err)
	}
	s.releaseRunWidth()

	gomutant.SetOracleMemoryLimit(-1, 1)
	before := gomutant.SnapshotOracleMemory()
	prior := gomutant.OracleMemoryLimitBytes()
	if prior != 0 {
		t.Fatalf("disabled ceiling reads %d", prior)
	}
	snap := gomutant.SnapshotOracleMemory()
	gomutant.SetOracleMemoryLimit(512<<20, 1)
	gomutant.RestoreOracleMemory(snap)
	if gomutant.OracleMemoryLimitBytes() != 0 || gomutant.SnapshotOracleMemory() != before {
		t.Fatal("restore did not reinstate the exact prior state")
	}
}
