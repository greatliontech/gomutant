package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A complete historical oracle does not establish independence from tests
// subsequently added to the same process. Both directions compare against
// a fresh complete execution, and check the actual dispatched scope.
func TestOracleGrowthRemeasuresInterferingTests(t *testing.T) {
	if testing.Short() {
		t.Skip("runs complete mutant oracles")
	}
	for _, explicit := range []bool{false, true} {
		for _, initiallyDisabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%t/disabled=%t", explicit, initiallyDisabled), func(t *testing.T) {
				dir := t.TempDir()
				write := func(name, source string) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				write("go.mod", "module example.com/interference\n\ngo 1.27\n")
				write("value.go", "package interference\nfunc Value(x int) int { return x+1 }\n")
				write("zz_test.go", fmt.Sprintf("package interference\nimport \"testing\"\nvar disabled = %t\nfunc TestZ(t *testing.T) { if !disabled && Value(1)!=2 { t.Fail() } }\n", initiallyDisabled))
				target := Target{Symbol: "example.com/interference.Value"}
				priors := map[string]Finding{}
				for _, change := range []string{"added", "edited"} {
					initialTarget := target
					if change == "edited" {
						write("aa_test.go", "package interference\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
					}
					if explicit {
						initialTarget.Oracle = []string{"example.com/interference.TestZ"}
						if change == "edited" {
							initialTarget.Oracle = append(initialTarget.Oracle, "example.com/interference.TestA")
						}
					}
					tree, err := Load(dir)
					if err != nil {
						t.Fatal(err)
					}
					prior, _, _ := executionPolicyRun(t, tree, initialTarget, Options{})
					wantOracles := 1
					if change == "edited" {
						wantOracles = 2
					}
					if len(prior.OracleEvidence) != wantOracles {
						t.Fatalf("%s prior has %d oracle rows, want %d", change, len(prior.OracleEvidence), wantOracles)
					}
					if prior.Generated == 0 || initiallyDisabled && len(prior.Survivors) == 0 || !initiallyDisabled && prior.Killed == 0 {
						t.Fatalf("%s initial control has no deciding outcomes: generated=%d killed=%d survivors=%d", change, prior.Generated, prior.Killed, len(prior.Survivors))
					}
					priors[change] = cloneFinding(prior)
				}
				write("aa_test.go", fmt.Sprintf("package interference\nimport \"testing\"\nfunc TestA(t *testing.T) { disabled = %t }\n", !initiallyDisabled))
				tree, err := Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				if explicit {
					target.Oracle = []string{"example.com/interference.TestA", "example.com/interference.TestZ"}
				}
				// Added and edited histories end at the same physical tree and
				// selection. One no-prior full execution is their common oracle.
				fresh, _, completeDispatch := executionPolicyRun(t, tree, target, Options{Force: true})
				for _, change := range []string{"added", "edited"} {
					t.Run(change, func(t *testing.T) {
						prior := cloneFinding(priors[change])
						var scopes [][]string
						current, decision, dispatched := executionPolicyRun(t, tree, target, Options{Prior: []Finding{prior}, executedScope: func(_, _ string, scope []string) { scopes = append(scopes, slices.Clone(scope)) }})
						for _, f := range []Finding{prior, current, fresh} {
							if f.Generated != f.Mutants+f.Discarded || f.Mutants != f.Killed+len(f.Survivors) || len(f.Kills) != f.Killed {
								t.Fatal("complete remeasurement failed candidate conservation or attribution")
							}
						}
						if current.Cached || decision.Action != "measure" || !slices.Equal(dispatched, completeDispatch) || len(scopes) == 0 {
							t.Fatalf("changed oracle not fully measured: decision=%+v cached=%t dispatch=%v full=%v scopes=%v", decision, current.Cached, dispatched, completeDispatch, scopes)
						}
						for _, scope := range scopes {
							if !slices.Equal(scope, []string{"^(TestA|TestZ)$"}) {
								t.Fatalf("incomplete oracle scope: %v", scope)
							}
						}
						if current.Killed != fresh.Killed || len(current.Survivors) != len(fresh.Survivors) || current.Discarded != fresh.Discarded || current.Generated != fresh.Generated {
							t.Fatalf("composition differs from full run: current=(%d,%d,%d,%d) full=(%d,%d,%d,%d)", current.Killed, len(current.Survivors), current.Discarded, current.Generated, fresh.Killed, len(fresh.Survivors), fresh.Discarded, fresh.Generated)
						}
						if initiallyDisabled && (current.Killed == 0 || len(current.Survivors) >= len(prior.Survivors)) || !initiallyDisabled && (current.Killed != 0 || len(current.Survivors) == 0) {
							t.Fatalf("shared-state control did not reverse outcomes: before=(%d,%d) after=(%d,%d)", prior.Killed, len(prior.Survivors), current.Killed, len(current.Survivors))
						}
						if current.OracleExecutionPolicy != "gomutant/full-oracle@3" {
							t.Fatalf("policy=%q", current.OracleExecutionPolicy)
						}
						served, _, dispatch := executionPolicyRun(t, tree, target, Options{Prior: []Finding{current}})
						if !served.Cached || len(dispatch) != 0 {
							t.Fatal("unchanged current-policy record did not serve")
						}

						// A policy-1 composition stamped current evidence over the old
						// outcomes. Its apparently exact current pins cannot repair them.
						contaminated := snapshotFindings([]Finding{current})[0]
						contaminated.OracleExecutionPolicy = "gomutant/full-oracle@1"
						contaminated.Killed, contaminated.Survivors = prior.Killed, slices.Clone(prior.Survivors)
						contaminated.Kills, contaminated.Operators = slices.Clone(prior.Kills), slices.Clone(prior.Operators)
						if ok, err := tree.Fresh(context.Background(), contaminated, target, 0); err != nil || ok {
							t.Fatalf("contaminated exact pins: fresh=%t err=%v", ok, err)
						}
						repaired, reason, dispatch := executionPolicyRun(t, tree, target, Options{Prior: []Finding{contaminated}})
						if repaired.Cached || !slices.Equal(dispatch, completeDispatch) || repaired.Killed != fresh.Killed || len(repaired.Survivors) != len(fresh.Survivors) {
							t.Fatalf("contaminated record survived remeasurement: %+v dispatch=%v", reason, dispatch)
						}
						executionPolicyRefusal(t, reason.Reason)
					})
				}
			})
		}
	}
}
