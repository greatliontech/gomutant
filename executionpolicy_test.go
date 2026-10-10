package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Each integration test owns its fixture: a changed test compartment must
// never alter the fixture another package's oracle is reading.
func executionPolicyTree(t *testing.T) (*Tree, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tree, dir
}

// The dispatch hook observes actual mutant work, not a decision's claimed
// count. No probe/schedule/executor is replaced by these tests.
func executionPolicyRun(t *testing.T, tree *Tree, target Target, opts Options) (Finding, RunDecision, []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var mu sync.Mutex
	var dispatched []int
	var decisions []RunDecision
	opts.Jobs = 1
	opts.Decision = func(d RunDecision) { decisions = append(decisions, d) }
	opts.dispatched = func(_ string, index int) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, index)
	}
	findings, err := tree.Run(ctx, []Target{target}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Skipped != "" || len(decisions) != 1 {
		t.Fatalf("run = %+v, decisions %+v; want one completed measurement or serve", findings, decisions)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(dispatched)
	return findings[0], decisions[0], slices.Clone(dispatched)
}

func executionPolicyRefusal(t *testing.T, reason string) {
	t.Helper()
	if !strings.Contains(reason, "oracle execution policy") || !strings.Contains(reason, "re-measure") {
		t.Fatalf("refusal = %q; want the execution policy and re-measure remedy", reason)
	}
}

// A historical body measurement cannot serve wholesale or contribute a
// budget prefix. Its disposition can ride only the newly executed result,
// with the policy move reported; the saved record itself stays historical.
func TestExecutionPolicyBodyReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real mutant oracles")
	}
	tree, _ := executionPolicyTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	current, _, firstDispatch := executionPolicyRun(t, tree, target, Options{Budget: 1})
	if current.OracleExecutionPolicy != FullOracleExecutionPolicy || !slices.Equal(firstDispatch, []int{0}) || len(current.Survivors) != 1 {
		t.Fatalf("control = %+v, dispatched %v; want a stamped, measured survivor", current, firstDispatch)
	}
	if ok, err := tree.Fresh(context.Background(), current, target, 1); err != nil || !ok {
		t.Fatalf("current Fresh = %v, %v", ok, err)
	}
	survivor := current.Survivors[0]
	if err := current.Attest(survivor.Position, survivor.Operator, "equivalent in the unexercised branch"); err != nil {
		t.Fatal(err)
	}
	served, _, dispatched := executionPolicyRun(t, tree, target, Options{Budget: 1, Prior: []Finding{current}})
	if !served.Cached || len(dispatched) != 0 {
		t.Fatalf("current exact serve = cached %v, dispatched %v", served.Cached, dispatched)
	}
	_, extension, dispatched := executionPolicyRun(t, tree, target, Options{Budget: 2, Prior: []Finding{current}})
	if !strings.Contains(extension.Reason, "prefix of 1 candidate stands") || !slices.Equal(dispatched, []int{1}) {
		t.Fatalf("current extension = %+v, dispatched %v", extension, dispatched)
	}
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		t.Run(fmt.Sprintf("policy=%q", policy), func(t *testing.T) {
			prior := current
			prior.OracleExecutionPolicy = policy
			if sameAttestationPins(prior, current) || sameAttestationPins(current, prior) {
				t.Fatal("policy move disappeared from attestation pins")
			}
			if ok, err := tree.Fresh(context.Background(), prior, target, 1); err != nil || ok {
				t.Fatalf("historical Fresh = %v, %v", ok, err)
			}
			before, err := Export([]Finding{prior}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, budget := range []int{1, 2} {
				var carries []AttestationCarry
				fresh, decision, dispatched := executionPolicyRun(t, tree, target, Options{
					Budget: budget, Prior: []Finding{prior},
					AttestationCarried: func(c AttestationCarry) { carries = append(carries, c) },
				})
				want := []int{0}
				if budget == 2 {
					want = []int{0, 1}
				}
				if fresh.Cached || decision.Action != "measure" || !slices.Equal(dispatched, want) || fresh.OracleExecutionPolicy != FullOracleExecutionPolicy {
					t.Fatalf("budget %d: decision %+v, cached %v, dispatch %v, policy %q", budget, decision, fresh.Cached, dispatched, fresh.OracleExecutionPolicy)
				}
				executionPolicyRefusal(t, decision.Reason)
				if !reflect.DeepEqual(fresh.Attested, current.Attested) || len(carries) != 1 || carries[0].Position != survivor.Position {
					t.Fatalf("fresh remeasurement lost or silently carried the historical disposition: attestations %+v, carries %+v", fresh.Attested, carries)
				}
			}
			flagged := prior
			flagged.CandidateEvidence = []CandidateEvidence{{Position: survivor.Position, Operator: survivor.Operator, Reason: "prior process incomplete", Disposition: "survived"}}
			fresh, decision, dispatched := executionPolicyRun(t, tree, target, Options{Budget: 2, Prior: []Finding{flagged}})
			if fresh.Cached || !slices.Equal(dispatched, []int{0, 1}) {
				t.Fatalf("flagged wider-budget history served: %+v, dispatch %v", decision, dispatched)
			}
			executionPolicyRefusal(t, decision.Reason)
			after, err := Export([]Finding{prior}, nil)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("re-measure rewrote the saved historical record: %v", err)
			}
		})
	}
}

func TestExecutionPolicyExplicitFreshFor(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real mutant oracles")
	}
	tree, _ := executionPolicyTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	const timeout = 30 * time.Second
	current, _, _ := executionPolicyRun(t, tree, target, Options{Budget: 1, OracleTimeout: timeout})
	if ok, err := tree.FreshFor(context.Background(), current, target, 1, timeout); err != nil || !ok {
		t.Fatalf("current explicit-timeout control = %v, %v", ok, err)
	}
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		prior := current
		prior.OracleExecutionPolicy = policy
		if ok, err := tree.FreshFor(context.Background(), prior, target, 1, timeout); err != nil || ok {
			t.Fatalf("policy %q FreshFor = %v, %v", policy, ok, err)
		}
	}
}

// Policy refusal is view-free, but does not override cancellation or the
// declaration check: a deleted body is detached rather than repairably stale.
func TestExecutionPolicyInspectionPrecedesViews(t *testing.T) {
	if testing.Short() {
		t.Skip("loads and measures the fixture")
	}
	tree, _ := executionPolicyTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	current, _, _ := executionPolicyRun(t, tree, target, Options{Budget: 1})
	if got, err := tree.InspectFinding(context.Background(), current, nil); err != nil || got.State != FindingCurrent {
		t.Fatalf("current inspection = %+v, %v", got, err)
	}
	oldBuild, oldSupplement := seams.subjectViewBuild, seams.inspectionSupplementaryView
	builds := 0
	seams.subjectViewBuild = func([]string) { builds++ }
	seams.inspectionSupplementaryView = func([]string) { builds++ }
	t.Cleanup(func() { seams.subjectViewBuild, seams.inspectionSupplementaryView = oldBuild, oldSupplement })
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		prior := current
		prior.OracleExecutionPolicy = policy
		got, err := tree.InspectFinding(context.Background(), prior, nil)
		if err != nil || got.State != FindingStale {
			t.Fatalf("policy %q inspection = %+v, %v", policy, got, err)
		}
		executionPolicyRefusal(t, got.Reason)
		batch, err := tree.InspectFindings(context.Background(), []Finding{prior}, nil)
		if err != nil || len(batch) != 1 || batch[0].State != FindingStale {
			t.Fatalf("batched policy inspection = %+v, %v", batch, err)
		}
		executionPolicyRefusal(t, batch[0].Reason)
		if recorded := RecordedInspection(prior); recorded.State != FindingRecorded {
			t.Fatalf("historical record became uninspectable: %+v", recorded)
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tree.InspectFinding(cancelled, prior, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled inspection = %v", err)
		}
		if _, err := tree.InspectFindings(cancelled, []Finding{prior}, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled batch = %v", err)
		}
		prior.Symbol = "example.com/fixture/lib.Deleted"
		if got, err := tree.InspectFinding(context.Background(), prior, nil); err != nil || got.State != FindingDetached {
			t.Fatalf("detached precedence = %+v, %v", got, err)
		}
	}
	if builds != 0 {
		t.Fatalf("policy-refused inspection built %d views", builds)
	}
}

// Added tests can preserve known kills, but no known kill is authoritative
// when its original execution policy is missing or unsupported.
func TestExecutionPolicyKillerDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real mutant oracles")
	}
	tree, dir := executionPolicyTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	current, _, fullDispatch := executionPolicyRun(t, tree, target, Options{Budget: 3})
	if current.Killed == 0 || len(current.Kills) != current.Killed || len(current.CandidateEvidence) != 0 || current.CompartmentLedger == nil {
		t.Fatalf("drift control lacks an attributable kill: %+v, dispatch %v", current, fullDispatch)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "executionpolicy_test.go"), []byte("package lib\n\nimport \"testing\"\n\n//gofresh:pure\nfunc TestPolicyAdded(t *testing.T) { if Add(0, 1) != 1 { t.Fatal(\"add\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target.Oracle = append(slices.Clone(target.Oracle), "example.com/fixture/lib.TestPolicyAdded")
	_, decision, dispatch := executionPolicyRun(t, tree, target, Options{Budget: 3, Prior: []Finding{current}})
	if len(dispatch) >= len(fullDispatch) || !strings.Contains(decision.Reason, "oracle grew") {
		t.Fatalf("sound grown-oracle control = %+v, dispatch %v", decision, dispatch)
	}
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		prior := current
		prior.OracleExecutionPolicy = policy
		fresh, decision, dispatch := executionPolicyRun(t, tree, target, Options{Budget: 3, Prior: []Finding{prior}})
		if fresh.Cached || decision.Action != "measure" || !slices.Equal(dispatch, fullDispatch) || fresh.OracleExecutionPolicy != FullOracleExecutionPolicy {
			t.Fatalf("policy %q drift reused historical kills: %+v, dispatch %v, policy %q", policy, decision, dispatch, fresh.OracleExecutionPolicy)
		}
		executionPolicyRefusal(t, decision.Reason)
	}
}

func TestExecutionPolicyFlaggedSplice(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real mutant oracles")
	}
	tree, _ := executionPolicyTree(t)
	target := Target{Symbol: "example.com/fixture/candlocal.Value", Oracle: []string{"example.com/fixture/candlocal.TestValue"}}
	current, _, fullDispatch := executionPolicyRun(t, tree, target, Options{})
	if len(current.CandidateEvidence) != 1 || len(fullDispatch) <= 1 {
		t.Fatalf("splice control = %+v, dispatch %v; want one flag and an unflagged candidate", current, fullDispatch)
	}
	served, decision, dispatch := executionPolicyRun(t, tree, target, Options{Prior: []Finding{current}})
	if !served.Cached || len(dispatch) != 1 || !strings.Contains(decision.Reason, "re-executing 1 candidate") {
		t.Fatalf("sound flagged-splice control = %+v, dispatch %v", decision, dispatch)
	}
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		prior := current
		prior.OracleExecutionPolicy = policy
		fresh, decision, dispatch := executionPolicyRun(t, tree, target, Options{Prior: []Finding{prior}})
		if fresh.Cached || decision.Action != "measure" || !slices.Equal(dispatch, fullDispatch) || fresh.OracleExecutionPolicy != FullOracleExecutionPolicy {
			t.Fatalf("policy %q spliced unflagged history: %+v, dispatch %v; want %v", policy, decision, dispatch, fullDispatch)
		}
		executionPolicyRefusal(t, decision.Reason)
	}
}

// A missing field is licensed by the recorded shape, not by a document
// version or by a lack of narrowed survivor buckets. Unknown policies are
// never licensed, even for a shape that still derives.
func TestExecutionPolicyHistoricalShapedReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real shaped mutant oracles")
	}
	tree, _ := executionPolicyTree(t)
	target := Target{Symbol: "recipe:policy-weak", OracleExplicit: true,
		Oracle: []string{"example.com/fixture/lib.TestWeak"},
		Manual: &ManualSpec{File: "lib/lib.go", Edits: []ManualEdit{{Find: "return x - 1", Replace: "return x + 1"}}}}
	current, _, _ := executionPolicyRun(t, tree, target, Options{})
	if current.Shape == nil || len(current.Survivors) != 1 || current.OracleExecutionPolicy != FullOracleExecutionPolicy {
		t.Fatalf("shaped control = %+v", current)
	}
	legacy := current
	legacy.OracleExecutionPolicy = ""
	data, err := Export([]Finding{legacy}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"version": 16`), []byte(`"version": 15`), 1)
	historical, err := ParseFindings(data)
	if err != nil || len(historical) != 1 || historical[0].OracleExecutionPolicy != "" || !reflect.DeepEqual(historical[0].Shape, current.Shape) {
		t.Fatalf("historical shaped record = %+v, %v", historical, err)
	}
	legacy = historical[0]
	for _, prior := range []Finding{current, legacy} {
		served, _, dispatched := executionPolicyRun(t, tree, target, Options{Prior: []Finding{prior}})
		if !served.Cached || len(dispatched) != 0 || served.OracleExecutionPolicy != prior.OracleExecutionPolicy {
			t.Fatalf("policy %q shaped serve = cached %v, policy %q, dispatch %v", prior.OracleExecutionPolicy, served.Cached, served.OracleExecutionPolicy, dispatched)
		}
		if got, err := tree.InspectFinding(context.Background(), prior, nil); err != nil || got.State != FindingCurrent {
			t.Fatalf("policy %q shaped inspection = %+v, %v", prior.OracleExecutionPolicy, got, err)
		}
	}
	s := legacy.Survivors[0]
	if err := legacy.Attest(s.Position, s.Operator, "the recipe changes only the unexercised branch"); err != nil {
		t.Fatalf("legacy shaped attestation refused: %v", err)
	}
	unknown := current
	unknown.OracleExecutionPolicy = "gomutant/future-oracle@999"
	got, err := tree.InspectFinding(context.Background(), unknown, nil)
	if err != nil || got.State != FindingStale {
		t.Fatalf("unknown shaped inspection = %+v, %v", got, err)
	}
	executionPolicyRefusal(t, got.Reason)
	fresh, decision, dispatched := executionPolicyRun(t, tree, target, Options{Prior: []Finding{unknown}})
	if fresh.Cached || len(dispatched) != 1 || decision.Action != "measure" || fresh.OracleExecutionPolicy != FullOracleExecutionPolicy {
		t.Fatalf("unknown shaped policy served: %+v, dispatch %v, policy %q", decision, dispatched, fresh.OracleExecutionPolicy)
	}
	// A policy-free record's shape is still a pin, not a blanket exemption.
	moved := target
	moved.Manual = &ManualSpec{File: "lib/lib.go", Edits: []ManualEdit{{Find: "return x - 1", Replace: "return x + 2"}}}
	for _, policy := range []string{"", FullOracleExecutionPolicy} {
		prior := legacy
		prior.OracleExecutionPolicy = policy
		fresh, _, dispatched = executionPolicyRun(t, tree, moved, Options{Prior: []Finding{prior}})
		if fresh.Cached || len(dispatched) != 1 || len(fresh.Attested) != 0 {
			t.Fatalf("policy %q changed shape retained evidence: cached=%v attested=%+v dispatch=%v", policy, fresh.Cached, fresh.Attested, dispatched)
		}
	}
}

func TestExecutionPolicyNewAttestation(t *testing.T) {
	base := survivorFinding("p.F")
	base.Attested = nil
	base.OracleExecutionPolicy = FullOracleExecutionPolicy
	s := base.Survivors[0]
	if err := base.Attest(s.Position, s.Operator, "control"); err != nil {
		t.Fatal(err)
	}
	for _, shaped := range []bool{false, true} {
		for _, policy := range []string{"", "gomutant/future-oracle@999"} {
			f := base
			f.Attested = nil
			f.OracleExecutionPolicy = policy
			if shaped {
				f.Shape = &TargetShape{Manual: &ManualSpec{File: "p.go", Edits: []ManualEdit{{Find: "old", Replace: "new"}}}}
			}
			err := f.Attest(s.Position, s.Operator, "new disposition")
			if shaped && policy == "" {
				if err != nil || len(f.Attested) != 1 {
					t.Fatalf("legacy shape = %+v, %v", f.Attested, err)
				}
				continue
			}
			if err == nil || len(f.Attested) != 0 {
				t.Fatalf("shape %v policy %q accepted a new disposition: %+v, %v", shaped, policy, f.Attested, err)
			}
			executionPolicyRefusal(t, err.Error())
		}
	}
	if !sameAttestationPins(base, base) {
		t.Fatal("equal policy pins do not match")
	}
	for _, policy := range []string{"", "gomutant/future-oracle@999"} {
		other := base
		other.OracleExecutionPolicy = policy
		if sameAttestationPins(base, other) || sameAttestationPins(other, base) {
			t.Fatalf("policy %q did not move the attestation pins", policy)
		}
	}
}

// The version bump does not invent an execution epoch. Interning,
// expansion, overlay admission, and current export preserve historical
// policy absence and historical dispositions as facts, including v15 rows.
func TestExecutionPolicyHistoricalStorage(t *testing.T) {
	if DocumentVersion != 16 {
		t.Fatalf("execution-policy reading boundary = %d, want 16", DocumentVersion)
	}
	for _, policy := range []string{"", FullOracleExecutionPolicy, "gomutant/future-oracle@999"} {
		t.Run(fmt.Sprintf("policy=%q", policy), func(t *testing.T) {
			original := survivorFinding("p.F")
			original.OracleExecutionPolicy = policy
			data, err := Export([]Finding{original}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(`"oracleExecutionPolicy"`)) != (policy != "") {
				t.Fatalf("wire policy presence disagrees with recorded fact: %s", data)
			}
			assertRecord := func(data []byte) {
				t.Helper()
				rows, err := ParseFindings(data)
				if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], original) {
					t.Fatalf("history changed through document parsing: %+v, %v; want %+v", rows, err, original)
				}
			}
			assertRecord(data)
			if policy == "" {
				inline, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				for version := 4; version <= 10; version++ {
					assertRecord(legacyRows(t, []byte(fmt.Sprintf(`{"version":%d,"findings":[%s]}`, version, inline))))
				}
			}
			// The pre-epoch document has the same content-keyed evidence
			// form; replace only the outer version, never re-encode its keys.
			if policy == "" {
				old := bytes.Replace(data, []byte(`"version": 16`), []byte(`"version": 15`), 1)
				if bytes.Equal(old, data) {
					t.Fatal("historical fixture did not change the version")
				}
				assertRecord(old)
				data = old
			}
			root := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			path := filepath.Join(root, "findings.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(path, root)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := store.Load(context.Background())
			if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], original) {
				t.Fatalf("historical store read = %+v, %v", rows, err)
			}
			if _, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) { return []Finding{original}, nil }); err != nil {
				t.Fatal(err)
			}
			overlay, err := os.ReadFile(store.entryPath(original.Symbol))
			if err != nil {
				t.Fatal(err)
			}
			assertRecord(overlay)
			if policy == "" {
				// Also exercise a pre-epoch machine-local entry, not just
				// the repository document's historical reader.
				if err := os.WriteFile(store.entryPath(original.Symbol), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := OpenStore(path, root)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = reopened.Load(context.Background())
			if err != nil || len(rows) != 1 || !reflect.DeepEqual(rows[0], original) {
				t.Fatalf("overlay reload = %+v, %v", rows, err)
			}
			if !reopened.Overlaid(original.Symbol) {
				t.Fatal("dirty record did not exercise the overlay")
			}
			exported, err := Export(rows, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertRecord(exported)
		})
	}
}
