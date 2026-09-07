package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
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
		"test_pkg": "example.com/fixture/lib", "run": "^TestWeak$", "oracle_timeout_sec": 60, "oracle_memory_mib": 512,
	}}
	params.SetProgressToken("tok")
	result, err := clientSession.CallTool(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("ephemeral tool errored: %+v", result)
	}
	// The probe ran under the ceiling the call asked for: the request's
	// knob reaches the probe's own bounds and the result states them
	// (REQ-exec-oracle-memory's per-run configuration).
	var decoded struct {
		OracleMemoryBytes int64 `json:"oracleMemoryBytes"`
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OracleMemoryBytes != 512<<20 {
		t.Fatalf("probe ran under ceiling %d, want the requested 512 MiB", decoded.OracleMemoryBytes)
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

// The run tool's execution events name the heartbeat's stretch, and the
// probe phase's announcement — priced — reaches a listening token; the
// ticks name the stretch only.
func TestRunStreamsNameTheProbePhase(t *testing.T) {
	var notified []string
	streams := newRunStreams(&runOut{}, func(m string) { notified = append(notified, m) })
	streams.executing(gomutant.ExecutionEvent{Phase: "probing", Symbol: "p.F", ProbesTotal: 3, EstimateProjected: "2m0s", ProbesUnpriced: 1})
	streams.executing(gomutant.ExecutionEvent{Phase: "probing", Symbol: "p.F", ProbesDone: 1, ProbesTotal: 3})
	if len(notified) != 1 || notified[0] != "probing p.F: 3 coverage probe(s) up to ~2m0s, 1 unpriced" {
		t.Fatalf("notifications after the announcement and a tick = %q; want the priced announcement alone", notified)
	}
	if got := streams.lastPhase.Load().(string); got != "probing 1/3 p.F" {
		t.Fatalf("heartbeat label during probing = %q", got)
	}
	streams.executing(gomutant.ExecutionEvent{Phase: "estimate", Symbol: "p.F", TargetIndex: 1, TargetCount: 1})
	if len(notified) != 2 || !strings.HasPrefix(notified[1], "estimate target 1/1 p.F") {
		t.Fatalf("notifications after the estimate = %q", notified)
	}
	if got := streams.lastPhase.Load().(string); got != "executing mutants p.F" {
		t.Fatalf("heartbeat label after the estimate = %q", got)
	}
}

// The run tool's execution events reach a listening token through its
// one execution hook: the executing announcement rides the
// notifications of a token-bearing run.
func TestToolRunForwardsExecutionEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a campaign over the fixture")
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
	params := &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"targets_json": `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/lib.TestAdd"]}]}`, "budget": 1, "oracle_timeout_sec": 120}}
	params.SetProgressToken("tok")
	result, err := clientSession.CallTool(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("run tool errored: %+v", result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		joined := strings.Join(messages, "\n")
		mu.Unlock()
		if strings.Contains(joined, "executing target 1/1 example.com/fixture/lib.Add") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run notifications = %q, want the executing announcement", joined)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
