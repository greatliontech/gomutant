package mcpserver

import (
	"runtime"
	"strings"
	"testing"
)

func TestInitializeIdentifiesTheRunningBinary(t *testing.T) {
	session := guidanceSession(t)
	info := session.InitializeResult().ServerInfo
	if info == nil || info.Name != "gomutant" {
		t.Fatalf("server identity: %+v", info)
	}
	for _, fact := range []string{"revision=", "modified=", "go=", "gofresh=", runtime.Version()} {
		if !strings.Contains(info.Version, fact) {
			t.Fatalf("initialize version lacks %q: %q", fact, info.Version)
		}
	}
}
