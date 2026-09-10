package gomutant

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The pair form parses, canonicalizes, and refuses: a bare package is
// unrepresentable (no colon), and control characters or a non-identifier
// variable refuse loudly instead of silently conferring nothing.
func TestParseDynamicStateVouches(t *testing.T) {
	got, err := ParseDynamicStateVouches([]string{"b.example/dep:Var", "a.example/dep:Var", "b.example/dep:Var"})
	if err != nil || len(got) != 2 || got[0] != "a.example/dep.Var" || got[1] != "b.example/dep.Var" {
		t.Fatalf("parse = %v, %v; want sorted deduplicated canonical pair", got, err)
	}
	for _, bad := range []string{
		"a.example/dep", "", ":Var", "a.example/dep:", "a.example/dep:not-ident",
		"a.example/dep:9lives", "a.example/dep:Var.Sub", "a.example/dep\x01x:Var", "a.example/dep :Var",
	} {
		if _, err := ParseDynamicStateVouches([]string{bad}); err == nil {
			t.Fatalf("malformed vouch %q accepted", bad)
		}
	}
}

// The tree's vouch set reaches inspection's analysis engines: a real
// pinned-dependency culprit (protobuf's global registries) makes a
// record's target evidence unverifiable under an unvouched tree, and
// the vouched tree lifts exactly that refusal — the same engines serve
// run verdicts, so the pin covers the whole analysis surface.
func TestVouchedTreeJudgesInspectionUnderTheSet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds gofresh views over the protobuf graph")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// protobuf is not in gomutant's own graph; any version already in
	// the shared module cache serves the fixture (GOPROXY=off).
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	cached, err := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "google.golang.org", "protobuf@v*"))
	if err != nil || len(cached) == 0 {
		t.Skipf("google.golang.org/protobuf absent from the module cache: %v %v", cached, err)
	}
	sort.Strings(cached)
	version := cached[len(cached)-1][strings.LastIndex(cached[len(cached)-1], "@")+1:]
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/vouchmut\n\ngo 1.26\n\nrequire google.golang.org/protobuf " + version + "\n",
		"reg.go": `package vouchmut

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func Count() int {
	n := 0
	protoregistry.GlobalFiles.RangeFiles(func(protoreflect.FileDescriptor) bool {
		n++
		return true
	})
	return n
}
`,
		"reg_test.go": `package vouchmut

import "testing"

func TestCount(t *testing.T) {
	if Count() < 0 {
		t.Fatal("count")
	}
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	ctx := context.Background()

	emptyManifest := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1}`))
	current, err := runtimeinput.CurrentEnv(emptyManifest, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	// Real closure hashes and guards, so inspection passes the staleness
	// ladder and reaches the dynamic-state judgment.
	plainEngine, err := gofresh.New(gofresh.WithDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	subjects := []gofresh.Subject{
		{Package: "example.com/vouchmut", Symbol: "Count"},
		{Package: "example.com/vouchmut", Symbol: "TestCount"},
	}
	view, err := plainEngine.NewView(ctx, subjects, dir)
	if err != nil {
		t.Fatal(err)
	}
	captured := map[string]gofresh.Fingerprint{}
	for _, subject := range subjects {
		fp, err := view.Capture(ctx, subject)
		if err != nil {
			t.Fatal(err)
		}
		captured["example.com/vouchmut."+subject.Symbol] = fp
	}
	evidence := func(symbol string) SubjectEvidence {
		fp := captured[symbol]
		return SubjectEvidence{Symbol: symbol, MaximalClosure: fp.MaximalClosure, TestVariantClosure: fp.TestVariantClosure,
			Toolchain: fp.Guards.Toolchain, BuildConfig: fp.Guards.BuildConfig, ObservationAssertion: "caller assertion",
			ObservationStrategy: "proof/v1", ObservationSubjectPackage: "example.com/vouchmut", ObservationSubjectSymbol: symbol,
			ObservationObservable: true, ObservationEvidence: "proof", DynamicStateVouches: fp.DynamicStateVouches,
			DynamicStateStrategy: fp.DynamicStateStrategy, RuntimeInputs: emptyManifest, RuntimeDigest: current.Digest}
	}
	finding := Finding{Symbol: "example.com/vouchmut.Count", BodyHash: "h", OperatorSet: engine.OperatorSet,
		OracleTimeout: "1m0s", Commit: "abc",
		TargetEvidence: evidence("example.com/vouchmut.Count"),
		OracleEvidence: []SubjectEvidence{evidence("example.com/vouchmut.TestCount")}}

	inspect := func(vouches ...string) string {
		t.Helper()
		tree, err := LoadContext(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(vouches) > 0 {
			tree.SetDynamicStateVouches(vouches...)
		}
		inspection, err := tree.InspectFinding(ctx, finding)
		if err != nil {
			t.Fatal(err)
		}
		return inspection.Reason
	}

	reason := inspect()
	if !strings.Contains(reason, "shares mutated dynamic state") {
		t.Fatalf("unvouched inspection reason = %q, want the dynamic-state downgrade", reason)
	}
	m := regexp.MustCompile(`([^\s:]+): ([^\s:]+)\.([\p{L}_][\p{L}\p{Nd}_]*) `).FindStringSubmatch(reason + " ")
	if m == nil || m[1] != m[2] {
		t.Fatalf("no culprit parsed from %q", reason)
	}
	culprit := m[1] + "." + m[3]

	vouchedReason := inspect(culprit)
	if strings.Contains(vouchedReason, culprit) {
		t.Fatalf("vouched inspection still names the culprit: %q", vouchedReason)
	}

	// The recorded discharge is never a serve input: evidence captured
	// under the vouched engine (the field non-empty) still refuses under
	// a plain tree - only the current engine's set governs.
	vouchedEngine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithDynamicStateVouches(culprit))
	if err != nil {
		t.Fatal(err)
	}
	vouchedView, err := vouchedEngine.NewView(ctx, subjects, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, subject := range subjects {
		fp, err := vouchedView.Capture(ctx, subject)
		if err != nil {
			t.Fatal(err)
		}
		captured["example.com/vouchmut."+subject.Symbol] = fp
	}
	if captured["example.com/vouchmut.Count"].DynamicStateVouches != culprit {
		t.Fatalf("vouched capture lacks the discharge: %+v", captured["example.com/vouchmut.Count"])
	}
	finding = Finding{Symbol: "example.com/vouchmut.Count", BodyHash: "h", OperatorSet: engine.OperatorSet,
		OracleTimeout: "1m0s", Commit: "abc",
		TargetEvidence: evidence("example.com/vouchmut.Count"),
		OracleEvidence: []SubjectEvidence{evidence("example.com/vouchmut.TestCount")}}
	if got := finding.TargetEvidence.DynamicStateVouches; got != culprit {
		t.Fatalf("recorded evidence discharge = %q", got)
	}
	withdrawnReason := inspect()
	if !strings.Contains(withdrawnReason, culprit) {
		t.Fatalf("withdrawn-vouch inspection = %q, want the recorded-discharge record refused naming %s", withdrawnReason, culprit)
	}
}

// The recorded vouches are audit metadata, not an attestation pin
// (the labels precedent): a vouch-set change alone never sheds a
// disposition, while any measured pin still does.
//
//gofresh:pure
func TestAttestationPinsIgnoreRecordedVouches(t *testing.T) {
	base := Finding{Symbol: "p.S", OperatorSet: "go/12", OracleTimeout: "1m0s",
		TargetEvidence: SubjectEvidence{Symbol: "p.S", MaximalClosure: "h"},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o"}}}
	vouched := base
	vouched.TargetEvidence.DynamicStateVouches = "a.example/dep.Var"
	vouched.OracleEvidence = []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o", DynamicStateVouches: "a.example/dep.Var"}}
	if !sameAttestationPins(base, vouched) {
		t.Fatal("a vouch-set change alone shed attestation pins")
	}
	moved := vouched
	moved.TargetEvidence.MaximalClosure = "h2"
	if sameAttestationPins(base, moved) {
		t.Fatal("a moved closure pin read as unchanged")
	}
}

// The discharge record crosses both evidence conversions: acceptance
// is auditable in the persisted findings document, never silent.
//
//gofresh:pure
func TestSubjectEvidenceCarriesDynamicStateVouches(t *testing.T) {
	fp := gofresh.Fingerprint{MaximalClosure: "h", DynamicStateVouches: "a.example/dep.Var"}
	e := evidenceFromFingerprint("p.S", fp, runtimeinput.State{})
	if e.DynamicStateVouches != "a.example/dep.Var" {
		t.Fatalf("evidence discharge = %q", e.DynamicStateVouches)
	}
	if back := e.fingerprint().DynamicStateVouches; back != "a.example/dep.Var" {
		t.Fatalf("fingerprint discharge = %q", back)
	}
}

// The oracle memory ceiling is a measurement pin exactly like the
// oracle timeout: attestation pins split on it, and the document
// version gates the narrowing field (REQ-exec-oracle-memory,
// REQ-result-record, REQ-result-export).
//
//gofresh:pure
func TestOracleMemoryPinGatesReuse(t *testing.T) {
	base := Finding{Symbol: "p.S", OperatorSet: "go/12", OracleTimeout: "1m0s", OracleMemoryBytes: 1 << 30,
		TargetEvidence: SubjectEvidence{Symbol: "p.S", MaximalClosure: "h"},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o"}}}
	// The pin is directional: attestations ride to a record assembled
	// under a ceiling at least as large — every verdict is preserved —
	// and refuse under a smaller one (REQ-result-stale's oracle-memory
	// clause).
	loosened := base
	loosened.OracleMemoryBytes = 0
	if !sameAttestationPins(base, loosened) {
		t.Fatal("an unlimited current ceiling refused attestations a bounded record's verdicts preserve")
	}
	tightened := base
	tightened.OracleMemoryBytes = 1 << 29
	if sameAttestationPins(base, tightened) {
		t.Fatal("a smaller current ceiling read as unchanged attestation pins")
	}
	// A ceiling-decided record pins its exact bytes: the ceiling
	// authored a verdict, so any different ceiling could flip one.
	decided := base
	decided.OracleCeilingDecided = true
	if sameAttestationPins(decided, loosened) {
		t.Fatal("a ceiling-decided record's attestations rode across a ceiling change")
	}
	if DocumentVersion < 4 {
		t.Fatalf("DocumentVersion = %d: the memory pin narrows reuse and rode the version-4 bump", DocumentVersion)
	}
}

// The measurement pin is the run's own bounds, derived once at entry
// and stamped: a campaign and any concurrent probe in the same process
// each carry their own, so no caller can move a run's ceiling under
// it (REQ-exec-oracle-memory, REQ-result-record).
func TestRunStampsItsOwnMemoryPin(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	want := engine.DefaultOracleMemoryLimit(1)
	if want == 0 {
		t.Skip("total RAM unreadable on this host")
	}
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}, Options{
		Budget: 1, Jobs: 1,
	})
	if err != nil || len(findings) != 1 {
		t.Fatalf("run = %+v, %v", findings, err)
	}
	if findings[0].OracleMemoryBytes != want {
		t.Fatalf("stamped pin = %d, want the run's derived %d", findings[0].OracleMemoryBytes, want)
	}
	explicit, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}, Options{
		Budget: 1, Jobs: 1, OracleMemoryBytes: 3 << 30,
	})
	if err != nil || len(explicit) != 1 || explicit[0].OracleMemoryBytes != 3<<30 {
		t.Fatalf("explicit run = %+v, %v; want the 3 GiB pin stamped", explicit, err)
	}
}

// Two probes in one process, each asking a different ceiling, spawn
// every one of their oracle processes — the baseline probe and the
// mutant runs alike — under their own bounds: bounds are a value each
// run carries, never process state a sibling's install could move
// (REQ-exec-oracle-memory, REQ-exec-attribution-symmetry's "differ in
// the overlay alone").
func TestConcurrentProbesSpawnUnderTheirOwnBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	original, err := os.ReadFile(filepath.Join(fixtureDir, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(original), "return a + b", "return a - b", 1)
	if broken == string(original) {
		t.Fatal("fixture Add body moved; update the replacement")
	}
	var mu sync.Mutex
	spawns := map[string][]engine.OracleBounds{} // run pattern → bounds seen by each spawn
	record := func(run string, bounds engine.OracleBounds) {
		mu.Lock()
		defer mu.Unlock()
		spawns[run] = append(spawns[run], bounds)
	}
	priorProbe, priorMutant := testProbe, runMutantEvidence
	defer func() { testProbe, runMutantEvidence = priorProbe, priorMutant }()
	testProbe = func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (int, bool, string, error) {
		record(run, bounds)
		return priorProbe(ctx, dir, testPkg, run, timeout, binFlags, env, bounds)
	}
	runMutantEvidence = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		record(runRegex, bounds)
		return priorMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, env, bounds)
	}
	probes := []struct {
		run    string
		memory int64
	}{{"^TestAdd$", 1 << 30}, {"^TestWeak$", 2 << 30}}
	var wg sync.WaitGroup
	errs := make([]error, len(probes))
	for i, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = tr.RunEphemeral(context.Background(), EphemeralRequest{File: "lib/lib.go", Mutant: []byte(broken), TestPkg: "example.com/fixture/lib", Run: p.run, OracleTimeout: time.Minute, Runs: 1, OracleMemoryBytes: p.memory})
		}()
	}
	wg.Wait()
	for i, p := range probes {
		if errs[i] != nil {
			t.Fatalf("probe %s: %v", p.run, errs[i])
		}
		seen := spawns[p.run]
		if len(seen) < 2 {
			t.Fatalf("probe %s spawned %d oracle processes, want its baseline and its mutant", p.run, len(seen))
		}
		for _, b := range seen {
			if b.MemoryBytes != p.memory || b.Width != runtime.NumCPU() {
				t.Fatalf("probe %s spawned under %+v, want its own ceiling %d and a lone tree's width %d", p.run, b, p.memory, runtime.NumCPU())
			}
		}
	}
}

// A campaign and a probe in one process each spawn under their own
// bounds: the campaign's every oracle process carries the ceiling and
// the width its job count derives, the probe's carry its own, and the
// campaign's stamped pin is the campaign's — a sibling call moves
// nothing (REQ-exec-oracle-memory's concurrent-runs clause,
// REQ-exec-oracle-parallelism's sibling-run clause, the MCP
// lifecycle's concurrency paragraph).
func TestCampaignAndProbeKeepTheirOwnBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	original, err := os.ReadFile(filepath.Join(fixtureDir, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(original), "return a + b", "return a - b", 1)
	var mu sync.Mutex
	var campaignSpawns, probeSpawns []engine.OracleBounds
	priorProbe, priorMutant, priorCampaign, priorBaseline := testProbe, runMutantEvidence, runMutantObservedEnv, groupBaselineProbe
	defer func() {
		testProbe, runMutantEvidence, runMutantObservedEnv, groupBaselineProbe = priorProbe, priorMutant, priorCampaign, priorBaseline
	}()
	testProbe = func(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (int, bool, string, error) {
		mu.Lock()
		probeSpawns = append(probeSpawns, bounds)
		mu.Unlock()
		return priorProbe(ctx, dir, testPkg, run, timeout, binFlags, env, bounds)
	}
	runMutantEvidence = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, string, string, error) {
		mu.Lock()
		probeSpawns = append(probeSpawns, bounds)
		mu.Unlock()
		return priorMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, env, bounds)
	}
	runMutantObservedEnv = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		mu.Lock()
		campaignSpawns = append(campaignSpawns, bounds)
		mu.Unlock()
		return priorCampaign(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env, bounds)
	}
	groupBaselineProbe = func(ctx context.Context, dir, pkg, run string, timeout time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error) {
		mu.Lock()
		campaignSpawns = append(campaignSpawns, bounds)
		mu.Unlock()
		return priorBaseline(ctx, dir, pkg, run, timeout, flags, moduleDir, packageDir, brackets, namespaces, env, bounds)
	}
	const campaignMemory, probeMemory = int64(3) << 30, int64(1) << 30
	var wg sync.WaitGroup
	var findings []Finding
	var campaignErr, probeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		findings, campaignErr = tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}, Options{Budget: 1, Jobs: 2, OracleMemoryBytes: campaignMemory})
	}()
	go func() {
		defer wg.Done()
		_, probeErr = tr.RunEphemeral(context.Background(), EphemeralRequest{File: "lib/lib.go", Mutant: []byte(broken), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: time.Minute, Runs: 1, OracleMemoryBytes: probeMemory})
	}()
	wg.Wait()
	if campaignErr != nil || probeErr != nil {
		t.Fatalf("campaign %v, probe %v", campaignErr, probeErr)
	}
	if len(findings) != 1 || findings[0].OracleMemoryBytes != campaignMemory {
		t.Fatalf("campaign stamped %+v, want its own %d pin beside the probe", findings, campaignMemory)
	}
	campaignBounds := engine.DeriveOracleBounds(campaignMemory, 2)
	if len(campaignSpawns) == 0 || len(probeSpawns) < 2 {
		t.Fatalf("spawns: campaign %d, probe %d — want both measured", len(campaignSpawns), len(probeSpawns))
	}
	for _, b := range campaignSpawns {
		if b != campaignBounds {
			t.Fatalf("campaign spawned under %+v beside the probe, want its own %+v", b, campaignBounds)
		}
	}
	for _, b := range probeSpawns {
		if b.MemoryBytes != probeMemory || b.Width != runtime.NumCPU() {
			t.Fatalf("probe spawned under %+v beside the campaign, want its own ceiling %d at a lone tree's width", b, probeMemory)
		}
	}
}

// The recorded closure-identity derivation is audit data beside the
// closure hashes, never a pin: a record grown the field on its first
// post-upgrade measure — or a derivation change — must not shed its
// dispositions over it (REQ-result-record's subject-evidence term).
//
//gofresh:pure
func TestAttestationPinsIgnoreRecordedClosureStrategy(t *testing.T) {
	base := Finding{Symbol: "p.S", OperatorSet: "go/12", OracleTimeout: "1m0s",
		TargetEvidence: SubjectEvidence{Symbol: "p.S", MaximalClosure: "h"},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o"}}}
	stamped := base
	stamped.TargetEvidence.ClosureStrategy = "gofresh/closure@1 gofresh/canonical-member@1 gofresh/variant-parse@1"
	stamped.OracleEvidence = []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o", ClosureStrategy: stamped.TargetEvidence.ClosureStrategy}}
	if !sameAttestationPins(base, stamped) {
		t.Fatal("a record grown the closure-strategy field shed its attestation pins")
	}
	rederived := stamped
	rederived.TargetEvidence.ClosureStrategy = "gofresh/closure@2 gofresh/canonical-member@1 gofresh/variant-parse@1"
	if !sameAttestationPins(stamped, rederived) {
		t.Fatal("a closure derivation change alone shed attestation pins")
	}
}

// The recorded package-process discharges are audit metadata exactly
// as the vouches: excluded from the attestation-pin comparison, so an
// execution-mode change alone never sheds a disposition.
//
//gofresh:pure
func TestAttestationPinsIgnoreRecordedPackageProcessDischarges(t *testing.T) {
	base := Finding{Symbol: "p.S", OperatorSet: "go/12", OracleTimeout: "1m0s",
		TargetEvidence: SubjectEvidence{Symbol: "p.S", MaximalClosure: "h"},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o"}}}
	discharged := base
	discharged.TargetEvidence.PackageProcessDischarges = "a.example/wire.reg"
	discharged.OracleEvidence = []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o", PackageProcessDischarges: "a.example/wire.reg"}}
	if !sameAttestationPins(base, discharged) {
		t.Fatal("a discharge-set change alone shed attestation pins")
	}
}

// The package-process attestation is honest exactly when every oracle
// symbol's package equals its target's — the pairing gate the run and
// the record inspections share.
//
//gofresh:pure
func TestPackageProcessAttestablePairing(t *testing.T) {
	cases := []struct {
		target string
		oracle []string
		want   bool
	}{
		{"example.com/mod/node.Host.Drain", []string{"example.com/mod/node.TestDrain"}, true},
		{"example.com/mod/node.Host.Drain", []string{"example.com/mod/node.TestA", "example.com/mod/other.TestB"}, false},
		{"example.com/mod/node.F", nil, true},
		{"example.com/mod/v2.F", []string{"example.com/mod/v2.TestF"}, true},
	}
	for _, tc := range cases {
		if got := packageProcessAttestable(tc.target, tc.oracle); got != tc.want {
			t.Fatalf("attestable(%s, %v) = %v, want %v", tc.target, tc.oracle, got, tc.want)
		}
	}
	f := Finding{Symbol: "example.com/mod/node.Host.Drain", OracleEvidence: []SubjectEvidence{
		{Symbol: "example.com/mod/node.TestDrain"}, {Symbol: "example.com/mod/other.TestB"},
	}}
	if findingPackageProcessAttestable(f) {
		t.Fatal("a cross-package oracle row read as attestable")
	}
}

// TestRunRecordsPackageProcessDischarges pins the integration end to
// end: a same-package derived-oracle run carries the package-process
// attestation, its binary-scoped discharge lifts a linked armer's
// downgrade, and the acceptance rides the evidence; an explicit
// cross-package oracle drops the attestation for the run, and the
// evidence carries no discharge.
func TestRunRecordsPackageProcessDischarges(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":            "module example.com/ppd\n\ngo 1.26\n",
		"wire/wire.go":      "package wire\n\nvar reg func() int\n\nfunc Arm() func() {\n\treg = func() int { return 1 }\n\treturn func() { reg = nil }\n}\n\nfunc Armed() bool { return reg != nil }\n",
		"wire/wire_test.go": "package wire\n\nimport \"testing\"\n\nfunc TestArmed(t *testing.T) {\n\tdisarm := Arm()\n\tdefer disarm()\n\tif !Armed() {\n\t\tt.Fail()\n\t}\n}\n",
		"gated.go":          "package gated\n\nimport \"example.com/ppd/wire\"\n\nfunc Gated(x int) int {\n\tif wire.Armed() {\n\t\treturn x\n\t}\n\treturn x + 1\n}\n\nfunc Two(x int) int {\n\treturn x + 2\n}\n",
		"gated_test.go":     "package gated\n\nimport \"testing\"\n\nfunc TestSmall(t *testing.T) {\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n\tif Two(5) != 7 {\n\t\tt.Fail()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	attested, err := tr.Run(ctx, []Target{{Symbol: "example.com/ppd.Gated"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := attested[0].TargetEvidence.PackageProcessDischarges; got != "example.com/ppd/wire.reg" {
		t.Fatalf("attested run recorded discharges %q, want the linked armer's culprit lifted and on the evidence", got)
	}
	crossTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cross, err := crossTree.Run(ctx, []Target{{
		Symbol: "example.com/ppd.Gated",
		Oracle: []string{"example.com/ppd.TestSmall", "example.com/ppd/wire.TestArmed"}, OracleExplicit: true,
	}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cross[0].Skipped != "" || cross[0].Generated == 0 {
		t.Fatalf("cross-package run did not measure: skipped=%q generated=%d", cross[0].Skipped, cross[0].Generated)
	}
	if got := cross[0].TargetEvidence.PackageProcessDischarges; got != "" {
		t.Fatalf("cross-package explicit oracle still recorded discharges %q — the pairing gate must drop the attestation", got)
	}
	// The semantics behind the strings: the attested record inspects
	// current (the armer's downgrade lifted and recorded), the cross
	// record unverifiable naming the armer's culprit — so a gofresh
	// change that kept emitting the discharge string while dropping the
	// lift would fail here, not silently pass.
	attestedInspection, err := crossTree.InspectFinding(ctx, attested[0])
	if err != nil {
		t.Fatal(err)
	}
	if attestedInspection.State != FindingCurrent {
		t.Fatalf("attested record inspects %v (%s), want current under the recorded discharge", attestedInspection.State, attestedInspection.Reason)
	}
	crossInspection, err := crossTree.InspectFinding(ctx, cross[0])
	if err != nil {
		t.Fatal(err)
	}
	if crossInspection.State != FindingUnverifiable || !strings.Contains(crossInspection.Reason, "example.com/ppd/wire.reg") {
		t.Fatalf("cross record inspects %v (%s), want unverifiable naming the armer's culprit", crossInspection.State, crossInspection.Reason)
	}

	// A MIXED run: the attestation is per target, so a cross-package
	// sibling in the same run never strips a same-package target's
	// discharge — the run builds one engine and view set per mode.
	mixedTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := mixedTree.Run(ctx, []Target{
		{Symbol: "example.com/ppd.Two"},
		{Symbol: "example.com/ppd.Gated",
			Oracle: []string{"example.com/ppd.TestSmall", "example.com/ppd/wire.TestArmed"}, OracleExplicit: true},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	bySymbol := map[string]Finding{}
	for _, f := range mixed {
		bySymbol[f.Symbol] = f
	}
	if got := bySymbol["example.com/ppd.Two"].TargetEvidence.PackageProcessDischarges; got != "example.com/ppd/wire.reg" {
		t.Fatalf("mixed run: the attested target recorded discharges %q — a cross-package sibling stripped a per-target attestation", got)
	}
	if got := bySymbol["example.com/ppd.Gated"].TargetEvidence.PackageProcessDischarges; got != "" {
		t.Fatalf("mixed run: the cross target recorded discharges %q", got)
	}
}
