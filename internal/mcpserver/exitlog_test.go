package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tearableTransport wraps the server's transport so a test can tear the
// wire under a live session: once torn, the connection's next read
// answers an error the protocol layer did not choose — the transport
// class of ending.
type tearableTransport struct {
	mcp.Transport
	torn chan struct{}
	mu   sync.Mutex // conn is set on the serve goroutine, read by tear
	conn mcp.Connection
}

func (t *tearableTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	return &tornConn{Connection: conn, torn: t.torn}, nil
}

// tear closes the torn channel BEFORE the connection, so any read
// returning after the tear takes the torn branch — deterministic.
func (t *tearableTransport) tear() {
	close(t.torn)
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()
	_ = conn.Close()
}

// wireTornAtCancel is a serve that failed before the session began,
// under a context that ended: its Connect waits for the serve context's
// end and then answers a wire error, which the protocol layer returns
// as its own before consulting the context — the deterministic shape
// of a wire error under a cancelled context.
type wireTornAtCancel struct{}

func (wireTornAtCancel) Connect(ctx context.Context) (mcp.Connection, error) {
	<-ctx.Done()
	return nil, errors.New("wire torn at connect")
}

// panickingTransport panics on Connect: a panic on the serve path
// itself, past nothing that recovers it but runOn.
type panickingTransport struct{}

func (panickingTransport) Connect(context.Context) (mcp.Connection, error) {
	panic("serve path boom")
}

type tornConn struct {
	mcp.Connection
	torn chan struct{}
}

func (c *tornConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	select {
	case <-c.torn:
		return nil, errors.New("wire torn under the session")
	default:
		return msg, err
	}
}

// serveOnce runs the server over an in-memory transport with a
// connected client that has made one call, and returns Run's error
// once the session ends the way end asks: the client closing the
// transport, the serve context cancelled, or the wire torn.
func serveOnce(t *testing.T, s *Server, end func(ctx context.Context, cancel context.CancelFunc, client *mcp.ClientSession, tear func())) error {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	wire := &tearableTransport{Transport: serverTransport, torn: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.runOn(ctx, wire) }()
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "findings", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	end(ctx, cancel, client, wire.tear)
	return <-done
}

func hostCloses(_ context.Context, _ context.CancelFunc, client *mcp.ClientSession, _ func()) {
	_ = client.Close()
}

func exitLog(t *testing.T, s *Server) string {
	t.Helper()
	data, err := os.ReadFile(s.ExitLogPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Every way a session ends leaves the exit line naming its class, the
// cause, the calls answered, and the uptime, beside the findings
// document, after the protocol layer's own lines; the host-initiated
// ends — its close, its signal — return no error, and a torn wire
// returns the transport class carrying exit code 2 (REQ-mcp-exit-log).
func TestExitLogNamesEveryEndOfASession(t *testing.T) {
	// The log is `.gomutant/mcp.log` under the server's directory,
	// whatever a call's own findings names; the notice writer is stderr.
	s := serverAt(t)
	if s.ExitLogPath() != filepath.Join(s.dir, ".gomutant", "mcp.log") {
		t.Fatalf("exit log at %s", s.ExitLogPath())
	}
	// The process's standard error stream by descriptor: under `go test
	// -json` the testing package points os.Stderr at stdout after
	// package init, so identity with os.Stderr is not the fact.
	if f, ok := exitLogNotice.(*os.File); !ok || f.Fd() != uintptr(syscall.Stderr) {
		t.Fatalf("the unwritable-log notice must reach the process's stderr in production: %T", exitLogNotice)
	}
	// The host closes the transport: host-closed, no error, the calls
	// answered — one of them naming another findings document, the log
	// staying put — and the protocol layer's session line in the log.
	if err := serveOnce(t, s, func(ctx context.Context, _ context.CancelFunc, client *mcp.ClientSession, _ func()) {
		if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "findings", Arguments: map[string]any{"findings": filepath.Join(s.dir, "elsewhere", "findings.json")}}); err != nil {
			t.Errorf("findings under another document: %v", err)
		}
		_ = client.Close()
	}); err != nil {
		t.Fatalf("host close returned %v", err)
	}
	log := exitLog(t, s)
	for _, want := range []string{"serve start", `msg="server session connected"`, `msg=exit class=host-closed cause="the host closed the transport" served=2 uptime=`} {
		if !strings.Contains(log, want) {
			t.Fatalf("host-closed log lacks %q: %q", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(s.dir, "elsewhere", "mcp.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a call's findings moved the log: %v", err)
	}
	// The serve context ends: cancelled, no error — a signal is the
	// host's stop, never a failure.
	s = serverAt(t)
	if err := serveOnce(t, s, func(_ context.Context, cancel context.CancelFunc, _ *mcp.ClientSession, _ func()) {
		cancel()
	}); err != nil {
		t.Fatalf("cancelled returned %v", err)
	}
	// The protocol layer answered the context's end with the context's
	// own error: the cause already, so no serve-error key.
	if log := exitLog(t, s); !strings.Contains(log, "msg=exit class=cancelled cause=\"context canceled\" served=1") || strings.Contains(log, "serve-error") {
		t.Fatalf("cancelled log = %q", log)
	}
	// The wire is torn under the session: transport, an ExitError of
	// that class whose exit code is 2, the cause the wire's.
	err := serveOnce(t, s, func(_ context.Context, _ context.CancelFunc, _ *mcp.ClientSession, tear func()) {
		tear()
	})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Class != ExitTransport || exit.MCPExitCode() != 2 || !strings.Contains(err.Error(), "wire torn under the session") {
		t.Fatalf("torn wire returned %v", err)
	}
	// On the transport arm the error IS the cause: no duplicate key.
	if log := exitLog(t, s); !strings.Contains(log, `msg=exit class=transport cause="wire torn under the session"`) || strings.Contains(log, `serve-error="wire torn`) {
		t.Fatalf("transport log = %q", log)
	}
	// Sessions append: a third session on the same document keeps the
	// prior lines, each session's served count its own.
	if err := serveOnce(t, s, hostCloses); err != nil {
		t.Fatal(err)
	}
	if log := exitLog(t, s); strings.Count(log, "msg=exit ") != 3 || strings.Count(log, "served=1 ") != 3 {
		t.Fatalf("appended log = %q, want three sessions' exit lines, one call each", log)
	}
}

// The class is decided from the two observed facts in order: a context
// that has ended is cancelled whatever the serve returned — the wire's
// error included, kept on the line as serve-error — a clean return is
// the host's close, any other error the transport (REQ-mcp-exit-log).
func TestExitClassIsDecidedByTheContextFirst(t *testing.T) {
	wire := errors.New("broken pipe")
	for _, row := range []struct {
		err, ctxErr error
		class       ExitClass
		cause       string
	}{
		{nil, nil, ExitHostClosed, "the host closed the transport"},
		{context.Canceled, context.Canceled, ExitCancelled, "context canceled"},
		{nil, context.Canceled, ExitCancelled, "context canceled"},
		{wire, context.DeadlineExceeded, ExitCancelled, "context deadline exceeded"},
		{wire, nil, ExitTransport, "broken pipe"},
	} {
		if class, cause := exitClass(row.err, row.ctxErr); class != row.class || cause != row.cause {
			t.Errorf("exitClass(%v, %v) = %s %q, want %s %q", row.err, row.ctxErr, class, cause, row.class, row.cause)
		}
	}
}

// A wire error surfaced under a cancelled context: the context's end
// decides the class and the wire's error — the one the class discarded
// — rides the line as serve-error, the serve returning no error
// (REQ-mcp-exit-log).
func TestCancelledServeKeepsTheWireErrorOnTheLine(t *testing.T) {
	s := serverAt(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.runOn(ctx, wireTornAtCancel{}) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a cancelled serve returned %v", err)
	}
	if log := exitLog(t, s); !strings.Contains(log, `msg=exit class=cancelled cause="context canceled" served=0`) || !strings.Contains(log, `serve-error="wire torn at connect"`) {
		t.Fatalf("racing log = %q", log)
	}
}

// The serve's own error rides the exit line exactly when the class
// discarded it: a cancelled serve the protocol layer answered with a
// wire error carries it; a serve whose error is the cause already —
// the transport arm, a cancellation answered with the context's error
// — and a clean serve carry no key (REQ-mcp-exit-log).
func TestServeErrorRidesTheLineOnlyWhenTheClassDiscardedIt(t *testing.T) {
	wire := errors.New("broken pipe")
	for _, row := range []struct {
		err    error
		ctxErr error
		want   []any
	}{
		{wire, context.Canceled, []any{"serve-error", "broken pipe"}},
		{context.Canceled, context.Canceled, nil},
		{wire, nil, nil},
		{nil, nil, nil},
		{nil, context.Canceled, nil},
	} {
		_, cause := exitClass(row.err, row.ctxErr)
		if got := serveErrorField(cause, row.err); !reflect.DeepEqual(got, row.want) {
			t.Errorf("serveErrorField(%q, %v) = %v, want %v", cause, row.err, got, row.want)
		}
	}
}

// A panic on the serve path itself writes the exit line with the panic
// class and cause and re-raises — the process then ends on the
// runtime's own panic exit (REQ-mcp-exit-log).
func TestServePathPanicIsLoggedAndReraised(t *testing.T) {
	s := serverAt(t)
	reraised := make(chan any, 1)
	go func() {
		defer func() { reraised <- recover() }()
		_ = s.runOn(context.Background(), panickingTransport{})
	}()
	if r := <-reraised; r != "serve path boom" {
		t.Fatalf("re-raised %v", r)
	}
	if log := exitLog(t, s); !strings.Contains(log, `level=ERROR msg=exit class=panic cause="serve path boom" served=0`) {
		t.Fatalf("panic log = %q", log)
	}
}

// A tool handler's panic never ends the session: it is recovered on
// the session's goroutine, logged with the call's method and cause,
// and answered as an error — counted as answered — the next call
// served (REQ-mcp-exit-log).
func TestHandlerPanicIsLoggedAndAnsweredAsAnError(t *testing.T) {
	s := serverAt(t)
	s.updateDocument = func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) error {
		panic("handler boom")
	}
	err := serveOnce(t, s, func(ctx context.Context, _ context.CancelFunc, client *mcp.ClientSession, _ func()) {
		res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "attest_survivor", Arguments: map[string]any{"symbol": "example.com/fixture/lib.Weak", "position": "lib.go:1:1", "operator": "x", "reason": "r"}})
		// The panic is ANSWERED, as an error naming it — never an empty
		// success and never a dropped call.
		answered := err != nil && strings.Contains(err.Error(), "panicked")
		if !answered && res != nil && res.IsError {
			for _, c := range res.Content {
				if text, ok := c.(*mcp.TextContent); ok && strings.Contains(text.Text, "panicked") {
					answered = true
				}
			}
		}
		if !answered {
			t.Errorf("the panicked call was not answered as an error naming the panic: err=%v res=%+v", err, res)
		}
		if _, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "findings", Arguments: map[string]any{}}); err != nil {
			t.Errorf("the session did not serve on after the panic: %v", err)
		}
		_ = client.Close()
	})
	if err != nil {
		t.Fatalf("session ended with %v after a handler panic", err)
	}
	log := exitLog(t, s)
	if !strings.Contains(log, `msg="handler panic" method=tools/call cause="handler boom"`) || !strings.Contains(log, "msg=exit class=host-closed") || !strings.Contains(log, "served=3") {
		t.Fatalf("panic log = %q", log)
	}
}

// The log is bounded at every write — a log at the bound rotates on a
// session's first line, and one session's writes rotate it mid-session
// — one generation kept; an unwritable log never fails serving and
// says so on the notice writer (REQ-mcp-exit-log).
func TestExitLogRotatesAndDegrades(t *testing.T) {
	s := serverAt(t)
	if err := os.MkdirAll(filepath.Dir(s.ExitLogPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ExitLogPath(), make([]byte, exitLogMaxBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := serveOnce(t, s, hostCloses); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(s.ExitLogPath() + ".1"); err != nil || info.Size() != exitLogMaxBytes {
		t.Fatalf("rotated generation = %v, %v", info, err)
	}
	if log := exitLog(t, s); !strings.Contains(log, "msg=exit class=host-closed") || len(log) > 4096 {
		t.Fatalf("fresh log after rotation = %d bytes: %q", len(log), log)
	}
	// Within one session: eight writes of a quarter of the bound each
	// rotate once, at the fifth — both generations exactly the bound.
	path := filepath.Join(t.TempDir(), "mcp.log")
	w, err := openRotatingFile(path, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := w.Write(bytes.Repeat([]byte{'x'}, 32)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".1"} {
		if info, err := os.Stat(p); err != nil || info.Size() != 128 {
			t.Fatalf("%s = %v, %v; want exactly the bound", p, info, err)
		}
	}
	// A directory at the log path is unwritable: serving proceeds, the
	// notice names the path.
	s = serverAt(t)
	if err := os.MkdirAll(s.ExitLogPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	prior := exitLogNotice
	exitLogNotice = &notice
	t.Cleanup(func() { exitLogNotice = prior })
	if err := serveOnce(t, s, hostCloses); err != nil {
		t.Fatalf("unwritable log failed serving: %v", err)
	}
	if !strings.Contains(notice.String(), "exit log "+s.ExitLogPath()+" unwritable") {
		t.Fatalf("notice = %q", notice.String())
	}
	var _ io.Writer = exitLogNotice
}
