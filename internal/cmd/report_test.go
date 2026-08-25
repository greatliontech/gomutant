package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

// The banked-state exit summary claims exactly the findings whose
// incremental commit returned — never in-flight work — so an
// interrupted run's report matches what the findings document holds
// (REQ-exec-cancellation's rendering half).
func TestBankedStateNamesOnlyCommittedFindings(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 5)
	rep.decision(gomutant.RunDecision{Action: "measure"})
	rep.decision(gomutant.RunDecision{Action: "cached"})
	rep.decision(gomutant.RunDecision{Action: "skipped"})
	rep.bankedFinding(gomutant.Finding{Symbol: "a.F", Killed: 3})
	rep.bankedFinding(gomutant.Finding{Symbol: "a.G", Killed: 1})
	rep.bankedState("command timeout")
	line := out.String()
	for _, want := range []string{"banked", "command timeout", "2 target(s) committed", "4 killed", "5 target(s)", "1 served", "1 skipped"} {
		if !strings.Contains(line, want) {
			t.Fatalf("banked line %q missing %q", line, want)
		}
	}
}

// The cadenced progress line reads cumulative run state: committed
// targets against the prepared count, the selection split, candidate
// tallies, kills.
func TestProgressLineRendersCumulativeState(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 85)
	rep.decision(gomutant.RunDecision{Action: "cached"})
	rep.executing(gomutant.ExecutionEvent{Phase: "executing", TargetCount: 42, CandidatesDone: 100, CandidatesTotal: 1826})
	rep.bankedFinding(gomutant.Finding{Symbol: "a.F", Killed: 7})
	rep.progressLine()
	line := out.String()
	for _, want := range []string{"progress", "1/85 targets committed", "1 served", "candidates 100/1826", "7 killed", "elapsed"} {
		if !strings.Contains(line, want) {
			t.Fatalf("progress line %q missing %q", line, want)
		}
	}
}

// The structured face emits one JSON object per line with the event
// kind stitched in — machine-readable without scraping the human
// rendering.
func TestJSONLEnvelopesParse(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, true, 3)
	rep.emit("decision", gomutant.RunDecision{Symbol: "a.F", Action: "measure", Candidates: 4})
	rep.emit("execution", gomutant.ExecutionEvent{Phase: "confirming", Symbol: "a.F", ConfirmationMode: "stride-sampled"})
	rep.bankedFinding(gomutant.Finding{Symbol: "a.F", Killed: 2})
	rep.bankedState("interrupt/cancellation")
	rep.progressLine()
	kinds := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %q is not one JSON object: %v", line, err)
		}
		kind, _ := env["event"].(string)
		if kind == "" {
			t.Fatalf("line %q carries no event kind", line)
		}
		kinds[kind] = true
	}
	for _, want := range []string{"decision", "execution", "banked", "progress"} {
		if !kinds[want] {
			t.Fatalf("event kinds %v missing %q", kinds, want)
		}
	}
}

// The execution line names the request beside a shrunken prepared
// count: a resumed run's "7/71" reads as remaining work of the same
// request, never a different campaign.
func TestExecutionEventNamesSelectionBesideDenominator(t *testing.T) {
	rep := newRunReporter(&bytes.Buffer{}, false, 85)
	var out bytes.Buffer
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "executing", TargetIndex: 7, TargetCount: 71, Symbol: "p.F", CandidatesDone: 1, CandidatesTotal: 2}, rep.selectionNote(71), "")
	if !strings.Contains(out.String(), "target 7/71 p.F (of 85 selected)") {
		t.Fatalf("line %q missing the selection context", out.String())
	}
	// Equal AND unoffset: the note is genuinely redundant, suppressed.
	out.Reset()
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "executing", TargetIndex: 7, TargetCount: 85, Symbol: "p.F", CandidatesDone: 1, CandidatesTotal: 2}, rep.selectionNote(85), "")
	if strings.Contains(out.String(), "selected") {
		t.Fatalf("line %q repeats an equal denominator", out.String())
	}
	// Coincidental equality — serves offset the count — keeps the note:
	// the context must not vanish exactly when it is ambiguous.
	rep.decision(gomutant.RunDecision{Action: "cached"})
	out.Reset()
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "executing", TargetIndex: 7, TargetCount: 85, Symbol: "p.F", CandidatesDone: 1, CandidatesTotal: 2}, rep.selectionNote(85), "")
	if !strings.Contains(out.String(), "(of 85 selected)") {
		t.Fatalf("line %q dropped the note under coincidental equality", out.String())
	}
}

// The confirmation mode renders when it first appears for a target or
// changes mid-target, once — the disarmed stride state is otherwise
// indistinguishable from the armed one in the log.
func TestConfirmationModeSuffixRendersOnChangeOnly(t *testing.T) {
	rep := newRunReporter(&bytes.Buffer{}, false, 1)
	e := gomutant.ExecutionEvent{Phase: "confirming", Symbol: "p.F", ConfirmationMode: "serial-full"}
	if got := rep.confirmationModeSuffix(e); got != "  mode=serial-full" {
		t.Fatalf("first mode suffix = %q", got)
	}
	if got := rep.confirmationModeSuffix(e); got != "" {
		t.Fatalf("repeated mode suffix = %q, want silent", got)
	}
	e.ConfirmationMode = "stride-sampled"
	if got := rep.confirmationModeSuffix(e); got != "  mode=stride-sampled" {
		t.Fatalf("mode-change suffix = %q", got)
	}
}

// gofresh's internal phase names render as operator vocabulary;
// unknown phases pass through raw rather than silently renaming.
func TestAnalysisPhraseVocabulary(t *testing.T) {
	if got := analysisPhrase("observe"); !strings.Contains(got, "freshness evidence") {
		t.Fatalf("observe phrase = %q", got)
	}
	if got := analysisPhrase("novel-phase"); got != "novel-phase" {
		t.Fatalf("unknown phase = %q, want passthrough", got)
	}
}

// A run interrupted mid-measurement renders the banked-state summary
// on its way out instead of ending on a bare context error — the exit
// paths (budget, signal, abort) share this cancellation route.
func TestRunCommandInterruptRendersBankedState(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/banked\n\ngo 1.26.5\n",
		"b.go":         "package banked\nfunc Value() int { return 1 }\n",
		"b_test.go":    "package banked\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/banked.Value","oracle":["example.com/banked.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := &triggerCancelWriter{cancel: cancel, trigger: []byte("measure")}
	err = runCommand(ctx, runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		budget: 1, output: out,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run = %v, want context.Canceled", err)
	}
	rendered := out.buf.String()
	// The banked line itself must carry the exit cause — "cancellation"
	// appearing elsewhere in the stream (a library error line) must
	// not satisfy this.
	if !strings.Contains(rendered, "banked    interrupt/cancellation after") {
		t.Fatalf("interrupted run output %q carries no banked-state summary naming the cause", rendered)
	}
	if !strings.Contains(rendered, "0 target(s) committed") {
		t.Fatalf("banked summary %q claims uncommitted work", rendered)
	}
}

// triggerActionWriter runs its action once, the first time the stream
// contains the trigger, capturing everything written.
type triggerActionWriter struct {
	buf     bytes.Buffer
	trigger []byte
	action  func()
	fired   bool
}

func (w *triggerActionWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if !w.fired && bytes.Contains(w.buf.Bytes(), w.trigger) {
		w.fired = true
		w.action()
	}
	return len(p), nil
}

// triggerCancelWriter cancels its context the first time the stream
// contains the trigger, capturing everything written.
type triggerCancelWriter struct {
	buf     bytes.Buffer
	cancel  context.CancelFunc
	trigger []byte
}

func (w *triggerCancelWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if bytes.Contains(w.buf.Bytes(), w.trigger) {
		w.cancel()
	}
	return len(p), nil
}

// A full run under the structured face emits ONLY JSON objects — no
// human line leaks into a stream a consumer parses.
func TestRunCommandJSONLEmitsOnlyJSON(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/jl\n\ngo 1.26.5\n",
		"j.go":         "package jl\nfunc Value() int { return 1 }\n",
		"j_test.go":    "package jl\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/jl.Value","oracle":["example.com/jl.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		budget: 1, jsonl: true, output: &out,
	}); err != nil {
		t.Fatalf("jsonl run: %v", err)
	}
	kinds := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("non-JSON line leaked into the structured face: %q (%v)", line, err)
		}
		if kind, _ := env["event"].(string); kind != "" {
			kinds[kind] = true
		}
	}
	for _, want := range []string{"decision", "result", "summary"} {
		if !kinds[want] {
			t.Fatalf("event kinds %v missing %q", kinds, want)
		}
	}
}

// A finding whose commit FAILED is work the findings document does
// not hold — the banked summary must not claim it
// (REQ-exec-cancellation's claims-only-committed clause). The
// unwritable store makes every commit fail while measurement
// succeeds.
func TestBankedStateExcludesFailedCommits(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/fc\n\ngo 1.26.5\n",
		"f.go":         "package fc\nfunc Value() int { return 1 }\n",
		"f_test.go":    "package fc\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/fc.Value","oracle":["example.com/fc.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fdir := filepath.Join(dir, "fdir")
	if err := os.Mkdir(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(fdir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	// The store and campaign lock open while the directory is still
	// writable; the chmod lands once measurement has started (the
	// first measure decision streams), so the COMMIT is what fails —
	// the claims-only-committed boundary, not a pre-measurement
	// refusal (which stays silent by contract).
	t.Cleanup(func() { _ = os.Chmod(fdir, 0o755) })
	out := &triggerActionWriter{trigger: []byte("measure"), action: func() { _ = os.Chmod(fdir, 0o555) }}
	err = runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		budget: 1, output: out,
	})
	if err == nil {
		t.Fatal("an unwritable findings store did not fail the run")
	}
	rendered := out.buf.String()
	if !strings.Contains(rendered, "banked    aborted") {
		t.Fatalf("abort exit rendered no banked-state summary: %q", rendered)
	}
	if !strings.Contains(rendered, "0 target(s) committed") {
		t.Fatalf("banked summary %q claims a finding whose commit failed", rendered)
	}
}

// The structured face stays pure on the paths the main purity test's
// fixture never reaches: plan mode and the no-targets path.
func TestRunCommandJSONLPlanAndNoTargetsStayPure(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/jp\n\ngo 1.26.5\n",
		"p.go":         "package jp\nfunc Value() int { return 1 }\n",
		"p_test.go":    "package jp\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/jp.Value","oracle":["example.com/jp.TestValue"]}]}`,
		"empty.json":   `{"targets":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	assertPure := func(t *testing.T, out string) {
		t.Helper()
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			var env map[string]any
			if err := json.Unmarshal([]byte(line), &env); err != nil {
				t.Fatalf("non-JSON line leaked: %q", line)
			}
		}
	}
	var plan bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		budget: 1, jsonl: true, plan: true, output: &plan,
	}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	assertPure(t, plan.String())
	var none bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "empty.json"), findingsFile: docPath,
		jsonl: true, output: &none,
	}); err != nil {
		t.Fatalf("no-targets: %v", err)
	}
	assertPure(t, none.String())
	if !strings.Contains(none.String(), `"no targets"`) {
		t.Fatalf("no-targets note missing from the structured stream: %q", none.String())
	}
}

// The cadence emits on its interval during a real run and its
// goroutine joins at stop: after runCommand returns, the writer
// receives nothing further.
func TestProgressCadenceEmitsAndJoins(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/cd\n\ngo 1.26.5\n",
		"c.go":         "package cd\nfunc Value() int { return 1 }\n",
		"c_test.go":    "package cd\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/cd.Value","oracle":["example.com/cd.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		budget: 1, progressEvery: 10 * time.Millisecond, output: &out,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "progress  ") {
		t.Fatal("no cadence line during a multi-second run at a 10ms interval")
	}
}

// stop joins the cadence goroutine: with a tick mid-write, stop must
// not return until the write completes — after stop, nothing can
// trail the epilogue or touch a writer the command returned from.
func TestCadenceStopJoinsAMidWriteTick(t *testing.T) {
	w := &blockingWriter{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
	rep := newRunReporter(w, false, 1)
	rep.startCadence(time.Millisecond)
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the cadence never ticked into a write")
	}
	stopped := make(chan struct{})
	go func() { rep.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while a cadence write was still in flight — no join")
	case <-time.After(50 * time.Millisecond):
	}
	close(w.gate)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop never returned after the write released")
	}
}

// blockingWriter parks every Write until released, signalling entry.
type blockingWriter struct {
	gate    chan struct{}
	entered chan struct{}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.gate
	return len(p), nil
}

// Every advisory class routed through the rep.line choke point stays
// pure on the structured face — the classes no integration fixture
// reaches (guidance, contradiction, sheds, carried, property) are
// pinned here, one table.
func TestReporterLineClassesStayPureUnderJSONL(t *testing.T) {
	classes := []struct {
		kind    string
		payload any
	}{
		{"guidance", gomutant.OracleGuidance{Symbol: "p.F", Reason: "r", Suggestion: "s"}},
		{"contradiction", gomutant.AttestationContradiction{Symbol: "p.F", Position: "f.go:1:1", Operator: "op", Killer: "T", Reason: "r"}},
		{"attestation-shed", gomutant.AttestationShed{Symbol: "p.F", Position: "f.go:1:1", Operator: "op", Reason: "r"}},
		{"attestation-carried", gomutant.AttestationCarry{Symbol: "p.F", Position: "f.go:1:1", Operator: "op"}},
		{"property", gomutant.PropertyOracleNote{Package: "p", Runtime: "rapid", Note: "n"}},
	}
	var out bytes.Buffer
	rep := newRunReporter(&out, true, 1)
	for _, c := range classes {
		rep.line(c.kind, c.payload, func(w io.Writer) { fmt.Fprintf(w, "raw human %s line\n", c.kind) })
	}
	kinds := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("class leaked a raw line: %q", line)
		}
		kind, _ := env["event"].(string)
		kinds[kind] = true
	}
	for _, c := range classes {
		if !kinds[c.kind] {
			t.Fatalf("kinds %v missing %q", kinds, c.kind)
		}
	}
}
