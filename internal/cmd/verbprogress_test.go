package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// fastCadence runs the one-call verbs' cadence at a millisecond for
// the test's duration.
func fastCadence(t *testing.T) {
	t.Helper()
	prior := verbProgressInterval
	verbProgressInterval = time.Millisecond
	t.Cleanup(func() { verbProgressInterval = prior })
}

func seedFinding(t *testing.T, dir, symbol, test, pkg string) {
	t.Helper()
	seed := gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: pkg,
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"},
		OracleEvidence: []gomutant.SubjectEvidence{{Symbol: test, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: pkg,
			ObservationSubjectSymbol: test, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}},
		Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors: []gomutant.Survivor{{Position: "lib/lib.go:1:1", Operator: "zero return"}}}
	if err := gomutant.UpdateDocument(findingsAt(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{seed}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The machine face stays a document: a judged findings --json under a
// cadence faster than its judge pass parses, with no progress line in
// the stream; the human face announces the load and ends on its own
// rows (REQ-exec-run-status).
func TestFindingsFacesUnderTheCadence(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	fastCadence(t)
	dir := isolatedFixture(t)
	seedFinding(t, dir, "example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd", "example.com/fixture/lib")
	var doc bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, judge: true, json: true}, &doc); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(doc.Bytes()) || strings.Contains(doc.String(), "progress") || strings.Contains(doc.String(), "prepare") {
		t.Fatalf("findings --json under the cadence is not a document: %q", doc.String())
	}
	var human bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings, judge: true}, &human); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(human.String(), "\n"), "\n")
	if lines[0] != "prepare   loading" || strings.HasPrefix(lines[len(lines)-1], "progress") {
		t.Fatalf("findings human face = %q; want the loading line first and the rows last", human.String())
	}
}

// Prune and retarget announce the load and end on their own rows: the
// cadence stops before the result renders.
func TestLifecycleVerbsAnnounceTheLoadAndEndOnTheirRows(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	fastCadence(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":    "module example.com/tiny\n\ngo 1.26.4\n",
		"p.go":      "package tiny\n\nfunc F(x int) int { return x }\n",
		"p_test.go": "package tiny\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedFinding(t, dir, "example.com/old.F", "example.com/old.TestF", "example.com/old")
	for name, run := range map[string]func(out *bytes.Buffer) error{
		"prune": func(out *bytes.Buffer) error {
			return pruneCommand(context.Background(), pruneOptions{dir: dir, findingsFile: defaultFindings, check: true}, out)
		},
		"retarget": func(out *bytes.Buffer) error {
			return retargetCommand(context.Background(), retargetOptions{dir: dir, findingsFile: defaultFindings, from: "example.com/old.", to: "example.com/tiny.", check: true}, out)
		},
	} {
		var out bytes.Buffer
		if err := run(&out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if lines[0] != "prepare   loading" || strings.HasPrefix(lines[len(lines)-1], "progress") {
			t.Fatalf("%s = %q; want the loading line first and the verb's rows last", name, out.String())
		}
	}
}

// The ephemeral verb's human face announces every phase, ends on the
// verdict, and an interruption reports the phase it cut short — the
// load included — after the cadence stops.
func TestEphemeralCommandReportsPhasesAndInterruptions(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	dir := isolatedFixture(t)
	replacement := filepath.Join(t.TempDir(), "lib.go")
	orig, err := os.ReadFile(filepath.Join(dir, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte(strings.Replace(string(orig), "return a + b", "return a - b", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := ephemeralOptions{dir: dir, file: "lib/lib.go", replacement: replacement, testPkg: "example.com/fixture/lib", runPat: "^TestAdd$", oracleTimeout: time.Minute, runs: 1, progressEvery: time.Millisecond, output: &out, oracleMemoryMiB: 768}
	if err := ephemeralCommand(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	// The verb's memory knob reaches the probe's own bounds and the
	// verdict states the ceiling the probe ran under.
	if !strings.Contains(out.String(), "oracle memory 768 MiB") {
		t.Fatalf("ephemeral output states no 768 MiB ceiling:\n%s", out.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if lines[0] != "prepare   loading" || !strings.Contains(out.String(), "prepare   baseline ^TestAdd$ example.com/fixture/lib 1m0s") || !strings.Contains(out.String(), "prepare   mutant-run 1/1") {
		t.Fatalf("ephemeral phases = %q", out.String())
	}
	verdict := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "killed") {
			verdict = i
		}
	}
	if verdict < 0 {
		t.Fatalf("no verdict line: %q", out.String())
	}
	for _, line := range lines[verdict:] {
		if strings.HasPrefix(line, "progress") {
			t.Fatalf("a progress line trails the verdict: %q", out.String())
		}
	}
	out.Reset()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ephemeralCommand(cancelled, o); err == nil {
		t.Fatal("cancelled probe succeeded")
	}
	if !strings.Contains(out.String(), "interrupted  context canceled during loading") {
		t.Fatalf("cancelled during the load = %q; want the interruption naming the load", out.String())
	}
}

// The run reporter's cadence yields the phase label to the tallies at
// the first decision.
func TestProgressLineYieldsToTheTalliesAtTheFirstDecision(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 0)
	rep.phase("preparing")
	rep.decision(gomutant.RunDecision{Symbol: "p.F", Action: "cached"})
	rep.progressLine()
	if !strings.Contains(out.String(), "targets committed") || strings.Contains(out.String(), "preparing") {
		t.Fatalf("progress after a decision = %q; want the tallies", out.String())
	}
	// A later target's preparation (the pipelined run's baseline probe)
	// never takes the line back from the tallies.
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationBaseline, Symbol: "p.G", Package: "example.com/p"})
	out.Reset()
	rep.progressLine()
	if !strings.Contains(out.String(), "targets committed") || strings.Contains(out.String(), "baseline") {
		t.Fatalf("progress after a later preparation = %q; want the tallies still", out.String())
	}
}
