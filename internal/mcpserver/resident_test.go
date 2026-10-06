package mcpserver

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// awaitReleases waits until the server's heap returns reach n.
func awaitReleases(t *testing.T, returns *atomic.Int64, n int64, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for returns.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: heap returned %d times, want %d", what, returns.Load(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// await receives one event or fails the test after five seconds —
// a pin that waits forever is a timeout, not a verdict.
func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no event within five seconds", what)
	}
}

// setStopped writes the serve-ended flag under its lock.
func (s *Server) setStopped(stopped bool) {
	s.idleMu.Lock()
	s.stopped = stopped
	s.idleMu.Unlock()
}

// The cached tree is released once no call has been in flight for the
// idle window — a burst inside the window reuses one load, a call in
// flight when the window elapses keeps it, the next call after the
// release reloads — the release returns the heap past its lock, and
// the window is the stated sixty seconds in production
// (REQ-mcp-resident-set).
func TestIdleServerReleasesItsTree(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	if defaultSeams().idleRelease != 60*time.Second {
		t.Fatalf("the idle window is %v, want the stated sixty seconds", defaultSeams().idleRelease)
	}
	s := serverAt(t)
	t.Cleanup(s.stopIdle)
	prior := seams
	t.Cleanup(func() { seams = prior })
	released := make(chan struct{}, 8)
	var heapReturns atomic.Int64
	seams.idleRelease = 200 * time.Millisecond
	seams.treeReleased = func(of *Server) {
		if of == s {
			released <- struct{}{}
		}
	}
	seams.heapReleased = func(of *Server) {
		if of == s {
			heapReturns.Add(1)
		}
	}
	ctx := context.Background()
	// call loads under a call and reports the tree it was served and
	// whether the load cached it (read under the call, before its end
	// arms the window).
	call := func() (*gomutant.Tree, bool) {
		t.Helper()
		s.callBegan()
		tree, err := s.loadTreeContext(ctx, gomutant.Selection{})
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		cached := s.tree != nil
		s.mu.Unlock()
		s.callEnded()
		return tree, cached
	}
	first, cached := call()
	if !cached {
		t.Fatal("the call left no cached tree")
	}
	// A burst inside the window reuses the one load.
	if again, _ := call(); again != first {
		t.Fatal("a second call inside the window did not reuse the cached tree")
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle window elapsed with the tree still cached")
	}
	s.mu.Lock()
	cached = s.tree != nil
	s.mu.Unlock()
	if cached {
		t.Fatal("the idle release left the tree cached")
	}
	awaitReleases(t, &heapReturns, 2, "the burst's return and the idle release")
	// A call in flight when the window elapses keeps the tree: the
	// pending release is cancelled at the call's start (a stale timer
	// would otherwise fire inside the NEXT call's window — an early
	// release), and a release firing under a call is a no-op.
	if _, cached := call(); !cached {
		t.Fatal("the reload left no cached tree")
	}
	s.callBegan()
	s.idleMu.Lock()
	pending := s.idle != nil
	s.idleMu.Unlock()
	if pending {
		t.Fatal("a call's start left the idle release pending")
	}
	if _, err := s.loadTreeContext(ctx, gomutant.Selection{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * seams.idleRelease)
	s.releaseIdle()
	s.mu.Lock()
	cached = s.tree != nil
	s.mu.Unlock()
	if !cached {
		t.Fatal("a release under an in-flight call dropped the tree")
	}
	select {
	case <-released:
		t.Fatal("the release fired while a call was in flight")
	default:
	}
	s.callEnded()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the window after the last call's end did not release the tree")
	}
	// The next call reloads — a new tree.
	if third, cached := call(); !cached || third == first {
		t.Fatalf("the call after the release: cached=%v, same tree=%v; want a fresh load", cached, third == first)
	}
	// The release returns the heap past its lock: a call that starts
	// while the release's collection runs (the observer held open)
	// never waits on it.
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the window after the reload did not release the tree")
	}
	var hold atomic.Pointer[chan struct{}]
	gate := func() chan struct{} {
		ch := make(chan struct{})
		hold.Store(&ch)
		return ch
	}
	held := gate()
	entered := make(chan struct{}, 4)
	seams.heapReleased = func(of *Server) {
		if of == s {
			heapReturns.Add(1)
			ch := *hold.Load()
			entered <- struct{}{}
			<-ch
		}
	}
	if _, cached := call(); !cached {
		t.Fatal("the reload left no cached tree")
	}
	await(t, entered, "the transition's own return")
	second := gate()
	close(held)
	await(t, entered, "the idle release's return")
	started := make(chan struct{})
	go func() {
		s.callBegan()
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(second) // let the held collection finish before failing
		t.Fatal("a call's start waited on the idle release's collection")
	}
	close(second)
	s.callEnded()
}

// A heap return begins when the LAST in-flight call ends — under no
// other call, off the reply path — through the receiving middleware,
// the panicked call ending as any other; at most one return is in
// progress and one pending at any moment (ends under a return fold
// into the pending one, and a last end is always followed by a return
// — the burst of three below is one instance); the serve's end stops
// the policy (REQ-mcp-resident-set). The return is the runtime's own:
// a dropped 64 MiB raises the released total.
func TestLastInFlightCallEndReturnsTheHeap(t *testing.T) {
	s := serverAt(t)
	t.Cleanup(s.stopIdle)
	prior := seams
	t.Cleanup(func() { seams = prior })
	var heapReturns atomic.Int64
	var hold atomic.Pointer[chan struct{}]
	gate := func() chan struct{} {
		ch := make(chan struct{})
		hold.Store(&ch)
		return ch
	}
	current := gate()
	entered := make(chan struct{}, 8)
	seams.idleRelease = time.Hour
	// Every return is held open until the test lets it finish (each
	// return takes the gate current at its entry, never one the test
	// has since replaced), so the coalescing is judged deterministically.
	seams.heapReleased = func(of *Server) {
		if of == s {
			heapReturns.Add(1)
			ch := *hold.Load()
			entered <- struct{}{}
			<-ch
		}
	}
	s.updateDocument = func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) (gomutant.Routing, error) {
		panic("handler boom")
	}
	err := serveOnce(t, s, func(ctx context.Context, _ context.CancelFunc, client *mcp.ClientSession, _ func()) {
		_, _ = client.CallTool(ctx, &mcp.CallToolParams{Name: "attest_survivor", Arguments: map[string]any{"symbol": "example.com/fixture/lib.Weak", "position": "lib.go:1:1", "operator": "x", "reason": "r"}})
		if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{}}); err != nil {
			t.Error(err)
		}
		_ = client.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	// serveOnce's findings call began the first return (held); the
	// panicked attest and the guidance call ended under it, marking
	// the trailing return — which the serve's end, before the first
	// completed, dropped: the policy ends with the serve. One return
	// for the session.
	await(t, entered, "a heap return")
	if got := heapReturns.Load(); got != 1 {
		t.Fatalf("a burst under a return in progress began %d returns, want one in progress", got)
	}
	next := gate()
	close(current)
	current = next
	time.Sleep(50 * time.Millisecond)
	s.idleMu.Lock()
	armed, inFlight, stopped, returning := s.idle != nil, s.inFlight, s.stopped, s.returning
	s.idleMu.Unlock()
	if armed || inFlight != 0 || !stopped || returning || heapReturns.Load() != 1 {
		t.Fatalf("after the session: idle release armed=%v, in flight=%d, stopped=%v, returning=%v, returns=%d", armed, inFlight, stopped, returning, heapReturns.Load())
	}
	// A sequential burst while serving: the first end begins a return
	// (held), the two ends under it mark ONE trailing return, run once
	// the first completes — two collections for the burst of three,
	// the last end followed by one.
	s.setStopped(false)
	for range 3 {
		s.callBegan()
		s.callEnded()
	}
	await(t, entered, "a heap return")
	if got := heapReturns.Load(); got != 2 {
		t.Fatalf("a burst of three ends began %d returns under the first, want one in progress", got-1)
	}
	next = gate()
	close(current)
	current = next
	await(t, entered, "a heap return")
	next = gate()
	close(current)
	current = next
	time.Sleep(50 * time.Millisecond)
	if got := heapReturns.Load(); got != 3 {
		t.Fatalf("a burst of three calls returned the heap %d times, want two (one in progress, one trailing)", got-1)
	}
	// Overlapping calls return once, at the last end; a call ending
	// after the serve arms nothing and returns nothing.
	s.callBegan()
	s.callBegan()
	s.callEnded()
	time.Sleep(50 * time.Millisecond)
	if heapReturns.Load() != 3 {
		t.Fatal("the heap was returned under another call in flight")
	}
	s.callEnded()
	await(t, entered, "a heap return")
	next = gate()
	close(current)
	current = next
	awaitReleases(t, &heapReturns, 4, "the last overlapping call's end")
	time.Sleep(50 * time.Millisecond)
	s.stopIdle()
	s.callBegan()
	s.callEnded()
	time.Sleep(50 * time.Millisecond)
	s.idleMu.Lock()
	armed = s.idle != nil
	s.idleMu.Unlock()
	if armed || heapReturns.Load() != 4 {
		t.Fatalf("after the serve ended: idle release armed=%v, heap returns %d (want 4)", armed, heapReturns.Load())
	}
	// The return is the runtime's own: freed heap is handed back to
	// the host, not left to the scavenger — a 64 MiB allocation
	// dropped before the last call's end raises the released total by
	// most of it.
	seams.heapReleased = func(of *Server) {
		if of == s {
			heapReturns.Add(1)
		}
	}
	s.setStopped(false)
	var start, end runtime.MemStats
	garbage := make([]byte, 64<<20)
	for i := range garbage {
		garbage[i] = byte(i)
	}
	runtime.GC()
	runtime.ReadMemStats(&start)
	garbage = nil
	s.callBegan()
	s.callEnded()
	awaitReleases(t, &heapReturns, 5, "the measured call's end")
	runtime.ReadMemStats(&end)
	if end.HeapReleased < start.HeapReleased+32<<20 {
		t.Fatalf("heap released grew %d bytes over a call's end after dropping 64 MiB, want the host to get it back", int64(end.HeapReleased)-int64(start.HeapReleased))
	}
}
