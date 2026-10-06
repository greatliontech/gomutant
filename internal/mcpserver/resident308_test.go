package mcpserver

import (
	"context"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/resident"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The serve-start line states the fleet ceiling the serve installed —
// the limit in force afterwards: the limit is lifted before the serve,
// so a serve installing nothing leaves none in force; the value is
// compared against what the serve stated, never against a second host
// derivation, which moves with the host (REQ-mcp-resident-set).
func TestServeStartInstallsAndStatesTheFleetCeiling(t *testing.T) {
	if _, ok := resident.HostMemory(); !ok {
		t.Skip("the host answers no memory reading: the fleet rule installs no ceiling here")
	}
	prior := seams
	t.Cleanup(func() { seams = prior })
	priorLimit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() { debug.SetMemoryLimit(priorLimit) })
	var mu sync.Mutex
	var installed []int64
	seams.memoryLimitInstalled = func(limit int64) {
		mu.Lock()
		defer mu.Unlock()
		installed = append(installed, limit)
	}
	s := serverAt(t)
	if err := serveOnce(t, s, hostCloses); err != nil {
		t.Fatal(err)
	}
	inForce := debug.SetMemoryLimit(-1)
	if inForce == math.MaxInt64 {
		t.Fatal("the serve left no memory limit in force")
	}
	mu.Lock()
	defer mu.Unlock()
	// The serve's install leads (the harness's one tool call re-derives
	// after it); the line states the serve's, and the last install is
	// the limit in force.
	if len(installed) < 2 || installed[len(installed)-1] != inForce {
		t.Fatalf("installs = %v, want the serve's then the call's, the last the limit in force %d", installed, inForce)
	}
	log := exitLog(t, s)
	if !strings.Contains(log, `msg="serve start"`) || !strings.Contains(log, "memory-limit="+strconv.FormatInt(installed[0], 10)) {
		t.Fatalf("serve start line does not state the serve's install %d: %q", installed[0], log)
	}
}

// A tool call that begins with none in flight re-derives the fleet
// ceiling, so a long-lived server's ceiling follows the host: the
// serve's own install, then one per such call (the harness's findings
// call included), each a derived ceiling; a call beginning under
// another in flight derives nothing — its own working set would count
// as the host's unavailable memory (REQ-mcp-resident-set).
func TestEveryToolCallReinstallsTheFleetCeiling(t *testing.T) {
	if _, ok := resident.HostMemory(); !ok {
		t.Skip("the host answers no memory reading: the fleet rule installs no ceiling here")
	}
	prior := seams
	t.Cleanup(func() { seams = prior })
	priorLimit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() { debug.SetMemoryLimit(priorLimit) })
	var mu sync.Mutex
	var installed []int64
	seams.memoryLimitInstalled = func(limit int64) {
		mu.Lock()
		defer mu.Unlock()
		installed = append(installed, limit)
	}
	s := serverAt(t)
	err := serveOnce(t, s, func(ctx context.Context, _ context.CancelFunc, client *mcp.ClientSession, _ func()) {
		for range 2 {
			if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{}}); err != nil {
				t.Error(err)
			}
		}
		_ = client.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(installed)
	}
	// The serve's install, the harness's own findings call, then the
	// two guidance calls: four.
	if got := count(); got != 4 {
		t.Fatalf("installs = %d, want the serve's and one per call (four)", got)
	}
	// An overlapping pair derives once, at the first; a serial pair
	// derives at each.
	s.callBegan()
	s.callBegan()
	s.callEnded()
	s.callEnded()
	if got := count(); got != 5 {
		t.Fatalf("installs after an overlapping pair = %d, want five (one for the pair)", got)
	}
	s.callBegan()
	s.callEnded()
	s.callBegan()
	s.callEnded()
	if got := count(); got != 7 {
		t.Fatalf("installs after a serial pair = %d, want seven", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, limit := range installed {
		if limit <= 0 {
			t.Fatalf("install %d reported %d, want a derived ceiling", i, limit)
		}
	}
}

// The heartbeat ends with the process's reading in the fleet's words —
// the same words the CLI's progress line carries; a host answering no
// reading leaves the line as it was (REQ-exec-run-status,
// REQ-mcp-resident-set).
func TestHeartbeatCarriesTheReadingInTheFleetsWords(t *testing.T) {
	prior := seams
	t.Cleanup(func() { seams = prior })
	seams.heartbeatInterval = 5 * time.Millisecond
	set := resident.Set{ProcessBytes: 10 << 20, ProcessPeakBytes: 20 << 20, CeilingBytes: 4 << 30}
	seams.residentSample = func() (resident.Set, bool) { return set, true }
	// Each arm collects into its own slice: a tick in flight when the
	// body returns delivers after it, so a shared slice would carry the
	// first arm's last line into the second.
	heartbeats := func() string {
		var mu sync.Mutex
		var notes []string
		_, _ = withHeartbeatLabel(context.Background(), func(message string) {
			mu.Lock()
			defer mu.Unlock()
			notes = append(notes, message)
		}, func() string { return "loading" }, func(context.Context) (int, error) {
			time.Sleep(60 * time.Millisecond)
			return 0, nil
		})
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(notes, "\n")
	}
	if joined, want := heartbeats(), resident.Suffix(set, "now"); !strings.Contains(joined, "still working: loading (") || !strings.Contains(joined, want) {
		t.Fatalf("heartbeat lines = %q, want the reading as their tail %q", joined, want)
	}
	seams.residentSample = func() (resident.Set, bool) { return resident.Set{}, false }
	if joined := heartbeats(); joined == "" || strings.Contains(joined, "resident") {
		t.Fatalf("a host without a reading: heartbeat lines = %q", joined)
	}
}
