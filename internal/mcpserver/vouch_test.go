package mcpserver

import (
	"context"
	"reflect"
	"testing"
)

// The server's vouch set reaches every tree it serves: the cache-miss
// load installs it, and the cached hit returns a tree installed at its
// own load - concurrent tool calls always judge under the server's set.
func TestServerInstallsVouchesOnLoadedTrees(t *testing.T) {
	s := serverAt(t)
	want := []string{"a.example/dep.Var"}
	s.vouches = append([]string(nil), want...)
	tree, err := s.loadTreeContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.DynamicStateVouches(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cache-miss tree vouches = %v, want %v", got, want)
	}
	cached, err := s.loadTreeContext(context.Background())
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
