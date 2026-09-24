package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/greatliontech/gomutant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A payload-bearing analysis event — a failing baseline's own output —
// is never discarded at the source: a request without a progress
// token keeps it inline, capped and counted; a request with one hears
// it as a notification and the response carries none inline
// (REQ-exec-run-status, REQ-mcp-envelope).
func TestToolRunKeepsAnalysisPayloadsInlineWithoutAToken(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the failing fixture package")
	}
	s := serverAt(t)
	targets := `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/failing.TestAlwaysFails"],"oracleExplicit":true}]}`
	_, out, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: targets, Budget: 1, OracleTimeoutSec: 60})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range out.AnalysisEvents {
		if a.Detail == "" {
			t.Fatalf("a detail-free keep-alive kept inline: %+v", a)
		}
		found = found || (a.Phase == "baseline-output" && strings.Contains(a.Detail, "TestAlwaysFails") && a.Package == "example.com/fixture/failing")
	}
	if !found || out.OmittedAnalysisEvents != 0 || out.AnalysisCount != len(out.AnalysisEvents) {
		t.Fatalf("tokenless analysis = %+v (omitted %d, count %d); want the failing baseline's output inline and the count honest", out.AnalysisEvents, out.OmittedAnalysisEvents, out.AnalysisCount)
	}
	// An aborted tokenless run has no response to carry them: the
	// payloads seen before the abort ride its error.
	aborted := serverAt(t)
	aborted.updateDocument = func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) error {
		return errors.New("the document write refused")
	}
	if _, _, err := aborted.toolRun(context.Background(), nil, runIn{TargetsJSON: targets, Budget: 1, OracleTimeoutSec: 60}); err == nil || !strings.Contains(err.Error(), "analysis payloads seen before this abort: baseline-output example.com/fixture/failing:") {
		t.Fatalf("aborted tokenless run = %v; want the payloads riding the error", err)
	}

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
	params := &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"targets_json": targets, "budget": 1, "oracle_timeout_sec": 60}}
	params.SetProgressToken("tok")
	result, err := clientSession.CallTool(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("run tool errored: %+v", result)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var streamedOut runOut
	if err := json.Unmarshal(encoded, &streamedOut); err != nil {
		t.Fatal(err)
	}
	if len(streamedOut.AnalysisEvents) != 0 || streamedOut.AnalysisCount == 0 {
		t.Fatalf("a streamed run kept analysis inline or lost its count: events %+v, count %d", streamedOut.AnalysisEvents, streamedOut.AnalysisCount)
	}
	mu.Lock()
	defer mu.Unlock()
	streamed := false
	for _, m := range messages {
		streamed = streamed || (strings.HasPrefix(m, "analysis ") && strings.Contains(m, "TestAlwaysFails"))
	}
	if !streamed {
		t.Fatalf("notifications = %q; want the failing baseline's output streamed", messages)
	}
}

// A contradiction — an attested survivor a re-measure's strengthened
// test killed — reaches a listening client as a notification, as sheds
// and carries do, beside its row on the response (REQ-attest-survivor's
// "loudly, in every mode"; REQ-mcp-envelope).
func TestToolRunNotifiesContradictions(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	s := serverAt(t)
	targets := `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`
	ctx := context.Background()
	_, first, err := s.toolRun(ctx, nil, runIn{TargetsJSON: targets, Budget: 1, OracleTimeoutSec: 120})
	if err != nil {
		t.Fatal(err)
	}
	measured, err := s.loadFindings("")
	if err != nil || len(measured) != 1 || len(measured[0].Survivors) == 0 {
		t.Fatalf("first measure = %+v, %v (response %+v)", measured, err, first.Summary)
	}
	survivor := measured[0].Survivors[0]
	if _, _, err := s.toolAttest(ctx, nil, attestIn{Symbol: measured[0].Symbol, Position: survivor.Position, Operator: survivor.Operator, Reason: "judged equivalent, wrongly"}); err != nil {
		t.Fatal(err)
	}
	libTest := filepath.Join(s.dir, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	strengthened := strings.Replace(string(src), `t.Fatal("small arm")`, "t.Fatal(\"small arm\")\n\t}\n\tif Weak(200) != 199 {\n\t\tt.Fatal(\"large arm\")", 1)
	if strengthened == string(src) {
		t.Fatal("TestWeak edit anchor missing")
	}
	if err := os.WriteFile(libTest, []byte(strengthened), 0o644); err != nil {
		t.Fatal(err)
	}
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
	// The moved oracle timeout beside the oracle edit forces the full
	// re-measure path rather than the killer-drift serve.
	params := &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"targets_json": targets, "budget": 1, "oracle_timeout_sec": 180}}
	params.SetProgressToken("tok")
	result, err := clientSession.CallTool(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("run tool errored: %+v", result)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"attestationContradictions":`) {
		t.Fatalf("the re-measure's response carries no contradiction row: %s", encoded)
	}
	mu.Lock()
	defer mu.Unlock()
	told := false
	for _, m := range messages {
		told = told || (strings.HasPrefix(m, "contradiction "+measured[0].Symbol+" ") && strings.Contains(m, "killed by"))
	}
	if !told {
		t.Fatalf("notifications = %q; want the contradiction told with its killer", messages)
	}
}

// The inline arm is one policy: payload-bearing events fill the row
// bound, the remainder is counted, every payload counts, and a
// detail-free keep-alive is neither kept nor counted
// (REQ-mcp-envelope).
func TestAnalysisInlineArmCapsAndCounts(t *testing.T) {
	var out runOut
	streams := newRunStreams(&out, nil)
	for i := 0; i < envelope.rows+2; i++ {
		streams.analysis(gomutant.AnalysisEvent{Phase: "freshness", Package: "p", Detail: fmt.Sprintf("payload %d", i)})
	}
	streams.analysis(gomutant.AnalysisEvent{Phase: "freshness", Package: "p"})
	if len(out.AnalysisEvents) != envelope.rows || out.OmittedAnalysisEvents != 2 || out.AnalysisCount != envelope.rows+2 {
		t.Fatalf("inline arm: kept %d, omitted %d, count %d; want %d, 2, %d", len(out.AnalysisEvents), out.OmittedAnalysisEvents, out.AnalysisCount, envelope.rows, envelope.rows+2)
	}
	// The first payloads are the kept ones, in order.
	for i, a := range out.AnalysisEvents {
		if a.Detail != fmt.Sprintf("payload %d", i) {
			t.Fatalf("kept payload %d = %q, want the first %d in order", i, a.Detail, envelope.rows)
		}
	}
}

// A cancellation before measurement began — the client's deadline
// expiring mid-preparation — has nothing banked and errors; the
// payloads recorded before it ride that error too (REQ-mcp-envelope).
func TestToolRunCancelledBeforeMeasurementCarriesTheAnalysisPayloads(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the failing fixture package")
	}
	s := serverAt(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The failing group's baseline is probed first (canonical package
	// order: failing before lib) and records its payload; the second
	// group's baseline stretch is the cancellation point — before any
	// execution event, so nothing is banked.
	baselines := 0
	seams.stretchObserver = func(label string) {
		if label == "prepare baseline" {
			if baselines++; baselines == 2 {
				cancel()
			}
		}
	}
	t.Cleanup(func() { seams.stretchObserver = nil })
	targets := `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/failing.TestAlwaysFails"],"oracleExplicit":true},{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`
	_, out, err := s.toolRun(ctx, nil, runIn{TargetsJSON: targets, Budget: 1, OracleTimeoutSec: 60})
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "analysis payloads seen before this abort: baseline-output example.com/fixture/failing:") {
		t.Fatalf("cancelled before measurement = %v (banked %v); want the cancellation carrying the payloads", err, out.Summary.Banked != nil)
	}
	if baselines < 2 {
		t.Fatalf("the second baseline stretch never came (%d seen); the fixture's shape moved", baselines)
	}
}

// The abort fold names each payload's subject and first line, up to
// five, and counts the remainder from the run's total — never from the
// capped inline list — with a package-less phase spelled bare.
func TestAnalysisRidingAbortCountsFromTheTotal(t *testing.T) {
	var events []gomutant.AnalysisEvent
	for i := 0; i < envelope.rows; i++ {
		events = append(events, gomutant.AnalysisEvent{Phase: "baseline-output", Package: "p", Detail: "TestX:\nsecond line"})
	}
	events[0] = gomutant.AnalysisEvent{Phase: "toolchain-unaudited", Detail: "go1.99 unlisted\nmore"}
	err := analysisRidingAbort(errors.New("aborted"), events, 200)
	want := "aborted; analysis payloads seen before this abort: toolchain-unaudited: go1.99 unlisted; baseline-output p: TestX:; baseline-output p: TestX:; baseline-output p: TestX:; baseline-output p: TestX: (+195 more)"
	if err == nil || err.Error() != want {
		t.Fatalf("abort fold = %v\nwant %s", err, want)
	}
	if err := analysisRidingAbort(errors.New("aborted"), nil, 0); err.Error() != "aborted" {
		t.Fatalf("an abort with no payload = %v", err)
	}
}

// A drift-refused tokenless run has no response to carry its recorded
// analysis payloads either: they ride the drift error beside the sheds
// and the persisted drop, so a failing baseline's own output reaches
// the reader whichever exit the run took (REQ-mcp-envelope).
func TestToolRunDriftExitCarriesTheAnalysisPayloads(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the failing fixture package and drifts the tree mid-run")
	}
	s := serverAt(t)
	libPath := filepath.Join(s.dir, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	seams.afterCommit = func(gomutant.Finding, string) {
		// The first commit lands; the tree moves under the target still
		// to be stamped.
		if err := os.WriteFile(libPath, append(append([]byte{}, src...), []byte("\nfunc Drifted() int { return 9 }\n")...), 0o644); err != nil {
			t.Error(err)
		}
		seams.afterCommit = nil
	}
	t.Cleanup(func() { seams.afterCommit = nil })
	targets := `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true},{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/failing.TestAlwaysFails"],"oracleExplicit":true},{"symbol":"example.com/fixture/lib.Guarded","oracle":["example.com/fixture/lib.TestGuarded"],"oracleExplicit":true}]}`
	_, out, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: targets, Budget: 1, OracleTimeoutSec: 60, Jobs: 1})
	if err == nil || !strings.Contains(err.Error(), "tree changed under measurement") {
		t.Fatalf("run over a tree moved after its first commit = %v (exit %q, findings %d); want the drift refusal", err, out.Exit, len(out.Findings))
	}
	if !strings.Contains(err.Error(), "analysis payloads seen before this abort: baseline-output example.com/fixture/failing:") {
		t.Fatalf("drift exit = %v; want the failing baseline's payload riding it", err)
	}
}
