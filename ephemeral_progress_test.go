package gomutant

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// An ephemeral probe reports its phases as they begin — the baseline
// probe with its leash, each mutant run with its budget, and the
// coverage probe when a run survived — in that order, and a request
// carries exactly one edit form (REQ-exec-run-status).
func TestRunEphemeralReportsItsPhases(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	tr := fixtureTree(t)
	orig, err := os.ReadFile("internal/engine/testdata/fixturemod/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(orig), "return a + b", "return a - b", 1)
	if broken == string(orig) {
		t.Fatal("fixture body not found")
	}
	var stages []string
	res, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "lib/lib.go", Mutant: []byte(broken), TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", OracleTimeout: time.Minute, Runs: 2,
		Progress: func(e PreparationEvent) {
			stages = append(stages, strings.TrimSpace(string(e.Stage)+" "+e.Symbol+" "+e.Package+" "+e.OracleBudget))
		}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed {
		t.Fatalf("the fixture's a - b mutant survived TestAdd: %+v", res)
	}
	want := []string{"baseline ^TestAdd$ example.com/fixture/lib 1m0s", "mutant-run 1/2 example.com/fixture/lib 1m0s", "mutant-run 2/2 example.com/fixture/lib 1m0s"}
	if strings.Join(stages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("phases = %q, want %q (a killed probe reaches no coverage phase)", stages, want)
	}
	if _, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "lib/lib.go", Mutant: []byte(broken), Edits: []Edit{{Old: "a", New: "b"}}, TestPkg: "example.com/fixture/lib", Run: "^TestAdd$"}); err == nil || !strings.Contains(err.Error(), "exactly one of") {
		t.Fatalf("two edit forms accepted: %v", err)
	}
	// The edits and batch forms carry the sink too; a surviving mutant
	// (Weak's untested branch) reaches the coverage phase.
	for name, req := range map[string]EphemeralRequest{
		"edits": {File: "lib/lib.go", Edits: []Edit{{Old: "return x - 1", New: "return x - 2"}}, TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1},
		"batch": {BatchEdits: []BatchEdit{{File: "lib/lib.go", OldString: "return x - 1", NewString: "return x - 2"}}, TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1},
	} {
		var stages []string
		req.Progress = func(e PreparationEvent) { stages = append(stages, string(e.Stage)+" "+e.Symbol) }
		res, err := tr.RunEphemeral(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Killed || strings.Join(stages, ",") != "baseline ^TestWeak$,mutant-run 1/1,coverage ^TestWeak$" {
			t.Fatalf("%s form: killed=%v phases=%q", name, res.Killed, stages)
		}
	}
}
