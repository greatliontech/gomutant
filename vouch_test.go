package gomutant

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

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
			ObservationObservable: true, ObservationEvidence: "proof", DynamicStateVouches: fp.DynamicStateVouches, RuntimeInputs: emptyManifest, RuntimeDigest: current.Digest}
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
		inspection, err := tree.InspectFindingContext(ctx, finding)
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
	loosened := base
	loosened.OracleMemoryBytes = 0
	if sameAttestationPins(base, loosened) {
		t.Fatal("a moved memory ceiling read as unchanged attestation pins")
	}
	if DocumentVersion != 4 {
		t.Fatalf("DocumentVersion = %d: the memory pin narrows reuse and must ride the version-4 bump", DocumentVersion)
	}
}

// The measurement pin is resolved once at run entry: a mid-campaign
// change to the process ceiling (a misbehaving concurrent caller)
// never diverges the stamped evidence from the compared pin
// (REQ-exec-oracle-memory, REQ-result-record).
func TestRunStampsTheResolvedMemoryPin(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	t.Cleanup(func() { engine.SetOracleMemoryLimit(-1, 1) })
	want := engine.DefaultOracleMemoryLimit(1)
	if want == 0 || want == 8<<30 {
		t.Skip("total RAM unreadable on this host")
	}
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}, Options{
		Budget: 1, Jobs: 1,
		Progress: func(PreparationEvent) {
			engine.SetOracleMemoryLimit(8<<30, 1)
		},
	})
	if err != nil || len(findings) != 1 {
		t.Fatalf("run = %+v, %v", findings, err)
	}
	if findings[0].OracleMemoryBytes != want {
		t.Fatalf("stamped pin = %d, want the entry-resolved %d (mid-campaign flip leaked)", findings[0].OracleMemoryBytes, want)
	}
}
