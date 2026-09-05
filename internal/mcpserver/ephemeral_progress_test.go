package mcpserver

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An ephemeral request carrying a progress token hears the probe's
// phases as they begin — the baseline probe, each mutant run, the
// coverage probe of a survivor — as preparation notifications
// (REQ-exec-run-status, REQ-mcp-envelope).
func TestToolEphemeralNotifiesItsPhases(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	var mu sync.Mutex
	var messages []string
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			if req.Params.ProgressToken == "tok" {
				messages = append(messages, req.Params.Message)
			}
		},
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	params := &mcp.CallToolParams{Name: "ephemeral", Arguments: map[string]any{
		"file": "lib/lib.go", "edits": []map[string]any{{"old": "return x - 1", "new": "return x - 2"}},
		"test_pkg": "example.com/fixture/lib", "run": "^TestWeak$", "oracle_timeout_sec": 60,
	}}
	params.SetProgressToken("tok")
	result, err := clientSession.CallTool(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("ephemeral tool errored: %+v", result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		joined := strings.Join(messages, "\n")
		mu.Unlock()
		if strings.Contains(joined, "prepare baseline ^TestWeak$") && strings.Contains(joined, "prepare mutant-run 1/1") && strings.Contains(joined, "prepare coverage ^TestWeak$") {
			if strings.Count(joined, "prepare loading") != 1 {
				t.Fatalf("progress notifications = %q, want exactly one loading announcement", joined)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress notifications = %q, want the probe's three phases", joined)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Every tool that loads announces `loading` at once when a token
// listens — before the heartbeat's first beat (REQ-exec-run-status).
func TestToolsAnnounceTheLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	s := New(tmp)
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	var mu sync.Mutex
	var messages []string
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			if req.Params.ProgressToken == "tok" {
				messages = append(messages, req.Params.Message)
			}
		},
	})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()
	params := &mcp.CallToolParams{Name: "discover", Arguments: map[string]any{"packages": []string{"example.com/fixture/lib"}}}
	params.SetProgressToken("tok")
	if _, err := clientSession.CallTool(ctx, params); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		joined := strings.Join(messages, "\n")
		mu.Unlock()
		if strings.Count(joined, "prepare loading") == 1 {
			return
		}
		if time.Now().After(deadline) || strings.Count(joined, "prepare loading") > 1 {
			t.Fatalf("discover notifications = %q, want exactly one loading announcement", joined)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
