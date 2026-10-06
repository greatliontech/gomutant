package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/resident"
)

// Every command's process runs under the fleet ceiling from the
// preamble, before the verb runs, whatever the verb: the limit is
// lifted before each command, so a preamble installing nothing leaves
// none in force; the limit the preamble reports is the one in force
// afterwards; and a verb refusing at its own preparation — before any
// load — has had the install already (a post-run install never runs
// for a refusing verb) (REQ-mcp-resident-set).
func TestEveryCommandInstallsTheFleetCeilingBeforeTheVerb(t *testing.T) {
	if _, ok := resident.HostMemory(); !ok {
		t.Skip("the host answers no memory reading: the fleet rule installs no ceiling here")
	}
	prior := seams
	t.Cleanup(func() { seams = prior })
	priorLimit := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(priorLimit) })
	var installed []int64
	seams.memoryLimitInstalled = func(limit int64) { installed = append(installed, limit) }
	for _, tc := range []struct {
		args    []string
		refuses bool
	}{
		{[]string{"version"}, false},
		{[]string{"discover", "--dir", t.TempDir()}, false},
		{[]string{"discover", "--dir", filepath.Join(t.TempDir(), "absent")}, true},
	} {
		installed = nil
		debug.SetMemoryLimit(math.MaxInt64)
		err := ExecuteContext(context.Background(), tc.args)
		if (err != nil) != tc.refuses {
			t.Fatalf("%v: err = %v, want refused=%v", tc.args, err, tc.refuses)
		}
		if len(installed) != 1 {
			t.Fatalf("%v: the preamble installed %d times, want once", tc.args, len(installed))
		}
		inForce := debug.SetMemoryLimit(-1)
		if inForce == math.MaxInt64 {
			t.Fatalf("%v: the preamble left no memory limit in force", tc.args)
		}
		if installed[0] != inForce {
			t.Fatalf("%v: the preamble reported %d, the limit in force is %d", tc.args, installed[0], inForce)
		}
	}
}

// The CLI progress line ends with the process's reading in the fleet's
// words on both of its branches — the phase line before the first
// decision and the tallies line after — and the structured record
// carries it; a host answering no reading leaves the line as it was
// (REQ-exec-run-status, REQ-mcp-resident-set).
func TestProgressLineCarriesTheReadingInTheFleetsWords(t *testing.T) {
	prior := seams
	t.Cleanup(func() { seams = prior })
	set := resident.Set{ProcessBytes: 10 << 20, ProcessPeakBytes: 20 << 20, Descendants: 2, DescendantsBytes: 30 << 20, DescendantPeakBytes: 25 << 20, CeilingBytes: 4 << 30}
	seams.residentSample = func() (resident.Set, bool) { return set, true }
	want := " — " + resident.Words(set, "now")
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 0)
	rep.phase("loading")
	rep.progressLine()
	if line := strings.TrimSpace(out.String()); !strings.HasPrefix(line, "progress  loading, elapsed ") || !strings.HasSuffix(line, want) {
		t.Fatalf("phase line = %q, want the reading as its tail %q", line, want)
	}
	// After the first decision: the tallies line, the reading last.
	out.Reset()
	rep.decided.Store(true)
	rep.progressLine()
	if line := strings.TrimSpace(out.String()); !strings.HasPrefix(line, "progress  0/0 targets committed") || !strings.HasSuffix(line, want) {
		t.Fatalf("tallies line = %q, want the reading as its tail %q", line, want)
	}
	// The structured face carries the same words on both branches —
	// the phase record before the first decision, the tallies record
	// after.
	var jsonOut bytes.Buffer
	structured := newRunReporter(&jsonOut, true, 0)
	structured.phase("loading")
	for _, branch := range []string{"phase", "tallies"} {
		jsonOut.Reset()
		if branch == "tallies" {
			structured.decided.Store(true)
		}
		structured.progressLine()
		var record struct {
			Event    string `json:"event"`
			Phase    string `json:"phase"`
			Resident string `json:"resident"`
		}
		if err := json.Unmarshal(jsonOut.Bytes(), &record); err != nil {
			t.Fatalf("structured progress (%s): %v: %q", branch, err, jsonOut.String())
		}
		if (branch == "phase") != (record.Phase != "") {
			t.Fatalf("structured %s record = %q, want the %s branch", branch, jsonOut.String(), branch)
		}
		if record.Resident != resident.Words(set, "now") {
			t.Fatalf("structured %s resident = %q, want %q", branch, record.Resident, resident.Words(set, "now"))
		}
	}
	// No reading: the line is unchanged.
	seams.residentSample = func() (resident.Set, bool) { return resident.Set{}, false }
	out.Reset()
	rep.progressLine()
	if line := strings.TrimSpace(out.String()); strings.Contains(line, "resident") {
		t.Fatalf("a host without a reading got one: %q", line)
	}
}

// The suites run with the operator's GOMEMLIMIT cleared — the fleet
// clause's consumer obligation (gofresh/resident): an environment
// carrying it suppresses the ceiling derivation for the whole process,
// and an oracle sets it on every test binary it runs, so a pin that
// expects a derived ceiling would read the oracle's word. The one
// suite entry (internal/testsuite.Main) clears it before the process's
// first derivation, which then runs capped at the limit the runtime
// read at startup — a derivation still; this pin reads the environment
// every suite runs in (REQ-mcp-resident-set).
func TestSuitesClearTheOperatorsMemoryLimit(t *testing.T) {
	if v, set := os.LookupEnv("GOMEMLIMIT"); set {
		t.Fatalf("GOMEMLIMIT=%q reached the suite: the suite entry must clear it before the first derivation", v)
	}
}
