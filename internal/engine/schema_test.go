package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
)

func TestMutantSchemaGenerationAndMerge(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/schemapkg\n\ngo 1.26.4\n",
		"a.go": `package schemapkg

func Add(x, y int) int {
Loop:
	for range [1]int{} {
		if x == 0 {
			break Loop
		}
	}
	return x + y
}

func Sub(x, y int) int {
	return x - y
}
`,
		"b.go": `package schemapkg

func Mul(x, y int) int {
	return x * y
}

func BrokenArray() int {
	a := [1]int{7}
	return a[0]
}
`,
		"pkg_test.go": `package schemapkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMath(t *testing.T) {
	if got := os.Getenv("GOMUTANT_ACTIVE_MUTANT"); got != "" {
		t.Fatalf("GOMUTANT_ACTIVE_MUTANT leaked into test environment: %q", got)
	}
	if base := filepath.Base(os.Args[0]); base != "schemapkg.test" && base != "schemapkg.test.exe" {
		t.Fatalf("filepath.Base(os.Args[0]) = %q, want schemapkg.test", base)
	}
	if strings.Contains(filepath.Base(os.Args[0]), "_gomutant_active_") {
		t.Fatalf("os.Args[0] basename leaked schema tag: %q", os.Args[0])
	}
	if Add(2, 3) != 5 {
		t.Fatal("Add failed")
	}
	if Sub(5, 3) != 2 {
		t.Fatal("Sub failed")
	}
	if Mul(3, 4) != 12 {
		t.Fatal("Mul failed")
	}
	if BrokenArray() != 7 {
		t.Fatal("BrokenArray failed")
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	addGen, err := tr.CandidatesContext(ctx, "example.com/schemapkg.Add", 0)
	if err != nil {
		t.Fatal(err)
	}
	subGen, err := tr.CandidatesContext(ctx, "example.com/schemapkg.Sub", 0)
	if err != nil {
		t.Fatal(err)
	}
	mulGen, err := tr.CandidatesContext(ctx, "example.com/schemapkg.Mul", 0)
	if err != nil {
		t.Fatal(err)
	}
	brokenGen, err := tr.CandidatesContext(ctx, "example.com/schemapkg.BrokenArray", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Verify BrokenArray's out-of-bounds a[1] candidate was filtered out of the schema by go/types.
	foundFilteredOutOfBounds := false
	for _, c := range brokenGen.Candidates {
		if len(c.Replacements) == 1 && strings.Contains(string(c.Replacements[0].Source), "a[1]") {
			if c.SchemaMutantID != "" || len(c.SchemaReplacements) != 0 {
				t.Fatalf("uncompilable candidate a[1] was included in schema: %+v", c)
			}
			foundFilteredOutOfBounds = true
		}
	}
	if !foundFilteredOutOfBounds {
		t.Fatal("expected BrokenArray to produce an a[1] candidate filtered from schema")
	}

	// Merge schemas across Add, Sub (both in a.go), Mul, and BrokenArray (both in b.go).
	MergeCandidateSchemas(addGen.Candidates, subGen.Candidates, mulGen.Candidates, brokenGen.Candidates)

	var sharedKey string
	schemaEligibleCount := 0
	for _, group := range [][]Candidate{addGen.Candidates, subGen.Candidates, mulGen.Candidates} {
		for _, c := range group {
			if len(c.Replacements) == 0 {
				continue
			}
			// Replacements must still hold the single-mutant source verbatim.
			if strings.Contains(string(c.Replacements[0].Source), "_gomutantActive_") {
				t.Fatalf("single-mutant Replacements[0] was polluted by schema source for %s", c.Symbol)
			}
			if c.SchemaMutantID == "" {
				continue
			}
			if c.SchemaKey == "" || len(c.SchemaReplacements) != 2 {
				t.Fatalf("candidate %s %s missing merged 2-file package schema: id=%q key=%q repls=%d",
					c.Symbol, c.Position, c.SchemaMutantID, c.SchemaKey, len(c.SchemaReplacements))
			}
			if sharedKey == "" {
				sharedKey = c.SchemaKey
			} else if c.SchemaKey != sharedKey {
				t.Fatalf("candidate %s got SchemaKey %q, want shared package SchemaKey %q", c.Symbol, c.SchemaKey, sharedKey)
			}
			schemaEligibleCount++
		}
	}
	if schemaEligibleCount < 8 {
		t.Fatalf("expected >=8 schema-eligible candidates across Add, Sub, and Mul, got %d", schemaEligibleCount)
	}

	if testing.Short() {
		t.Skip("runs go test -c and schema binary execution")
	}

	bounds := DeriveOracleBounds(-1, 2)
	defer bounds.Close()

	moduleDir, packageDir, err := tr.PackageContext("example.com/schemapkg")
	if err != nil {
		t.Fatal(err)
	}
	env := tr.GoEnv()
	_, passed, _, _, baseObs, err := TestProbeObservedEnv(ctx, dir, "example.com/schemapkg", "^TestMath$", time.Minute, nil, moduleDir, packageDir, nil, nil, env, bounds)
	if err != nil || !passed || !baseObs.OK || baseObs.Unverifiable {
		t.Fatalf("baseline probe failed: passed=%v obs=%+v err=%v", passed, baseObs, err)
	}

	observations := []gofresh.Observation{baseObs}
	for _, group := range [][]Candidate{addGen.Candidates, subGen.Candidates, mulGen.Candidates, brokenGen.Candidates} {
		for _, c := range group {
			m, ok := c.Mutant()
			if !ok {
				continue
			}
			outSchema, killerSchema, _, obsSchema, incSchema, diagSchema, err := RunMutantObservedEnv(
				ctx, dir, m, []string{"example.com/schemapkg"}, "^TestMath$", time.Minute,
				nil, moduleDir, packageDir, nil, nil, env, bounds,
			)
			if err != nil {
				t.Fatalf("schema run %s %s (%s): %v", m.Symbol, m.Position, m.Operator, err)
			}
			// Compare against non-schema execution (zero OracleBounds has nil Schemas).
			outStd, killerStd, _, _, _, diagStd, err := RunMutantObservedEnv(
				ctx, dir, m, []string{"example.com/schemapkg"}, "^TestMath$", time.Minute,
				nil, moduleDir, packageDir, nil, nil, env, OracleBounds{},
			)
			if err != nil {
				t.Fatalf("std run %s %s (%s): %v", m.Symbol, m.Position, m.Operator, err)
			}
			if (c.SchemaMutantID == "") != (outStd == MutantDiscarded) {
				t.Fatalf("mutant %s %s (%s) go/types schema filter disagreed with compiler: SchemaMutantID=%q outStd=%v",
					m.Symbol, m.Position, m.Operator, c.SchemaMutantID, outStd)
			}
			if outSchema != outStd || killerSchema != killerStd {
				t.Fatalf("mutant %s %s (%s) verdict mismatch: schema=(%v, %q) vs std=(%v, %q)",
					m.Symbol, m.Position, m.Operator, outSchema, killerSchema, outStd, killerStd)
			}
			if outStd == MutantDiscarded && (diagSchema == "" || diagStd == "") {
				t.Fatalf("expected compiler diagnostic on discarded mutant %s %s: schema=%q std=%q",
					m.Symbol, m.Position, diagSchema, diagStd)
			}
			if outSchema != MutantDiscarded {
				if incSchema != "" || !obsSchema.OK || obsSchema.Unverifiable {
					t.Fatalf("schema observation for %s %s incomplete=%q obs=%+v", m.Symbol, m.Position, incSchema, obsSchema)
				}
				observations = append(observations, obsSchema)
			}
		}
	}

	// Verify that only ONE schema binary was compiled for the entire package.
	bounds.Schemas.mu.Lock()
	compiledCount := len(bounds.Schemas.entries)
	bounds.Schemas.mu.Unlock()
	if compiledCount != 1 {
		t.Fatalf("SchemaCache compiled %d binaries, want 1 coalesced binary for the package", compiledCount)
	}

	// Verify gofresh.MergeObservations across baseline and all schema mutant runs stays verifiable
	// (proving GOMUTANT_ACTIVE_MUTANT never leaked into testlog).
	merged, err := gofresh.MergeObservations(dir, env, observations...)
	if err != nil {
		t.Fatalf("gofresh.MergeObservations failed: %v", err)
	}
	if !merged.OK || merged.Unverifiable {
		t.Fatalf("merged observation unverifiable (GOMUTANT_ACTIVE_MUTANT leaked into testlog?): merged=%+v", merged)
	}
	current, err := gofresh.CurrentRuntimeInputs(ctx, merged.Manifest, dir, env)
	if err != nil || !current.OK || current.Unverifiable || current.Digest != merged.Digest {
		t.Fatalf("merged observation current check failed: current=%+v merged=%+v err=%v", current, merged, err)
	}
}

func TestSchemaRejectsSignatureVarShortRedeclaration(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/redeclpkg\n\ngo 1.26.4\n",
		"redecl.go": `package redeclpkg

func step(v int) (int, int) {
	return v + 1, v * 2
}

func Compute(x int) int {
	readX := func() int { return x }
	y, x := step(x)
	return y + readX()
}

func ComputeLabeled(x int) (res int) {
	defer func() { res += x }()
L:
	y, x := step(x)
	if y < 0 {
		goto L
	}
	return y
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, sym := range []string{"example.com/redeclpkg.Compute", "example.com/redeclpkg.ComputeLabeled"} {
		gen, err := tr.CandidatesContext(ctx, sym, 0)
		if err != nil {
			t.Fatalf("%s: %v", sym, err)
		}
		if len(gen.Candidates) == 0 {
			t.Fatalf("expected candidates for %s", sym)
		}
		for _, c := range gen.Candidates {
			if c.SchemaMutantID != "" || len(c.SchemaReplacements) != 0 {
				t.Fatalf("expected %s candidate %s (%s) to refuse schema due to signature var := redeclaration, got id=%q",
					sym, c.Position, c.Operator, c.SchemaMutantID)
			}
		}
	}
}

func TestSchemaFiltersCandidateStrandingDotImport(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/dotimportpkg\n\ngo 1.26.4\n",
		"dot.go": `package dotimportpkg

import . "strings"

func Shout(s string) string {
	if s == "" {
		return ToUpper(s)
	}
	return s
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := tr.CandidatesContext(context.Background(), "example.com/dotimportpkg.Shout", 0)
	if err != nil {
		t.Fatal(err)
	}
	sawExcluded := false
	sawIncluded := false
	for _, c := range gen.Candidates {
		if len(c.Replacements) == 0 {
			continue
		}
		src := string(c.Replacements[0].Source)
		if !strings.Contains(src, "ToUpper") {
			if c.SchemaMutantID != "" || len(c.SchemaReplacements) != 0 {
				t.Fatalf("candidate stranding dot import should be excluded from schema: %s (%s)\n%s",
					c.Position, c.Operator, src)
			}
			sawExcluded = true
		} else if c.SchemaMutantID != "" {
			sawIncluded = true
		}
	}
	if !sawExcluded {
		t.Fatal("expected at least one candidate stranding dot import ToUpper to be excluded from schema")
	}
	if !sawIncluded {
		t.Fatal("expected compilable candidates preserving ToUpper to remain in schema")
	}
}

func TestSchemaCrossFileVarInitAndEarlyInitUnsetenv(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test -c and schema binary execution")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/crossinit\n\ngo 1.26.4\n",
		"a.go": `package crossinit

import "os"

type Adder interface {
	Val(int) int
}

var GlobalVal = Adder(impl{}).Val(10)

var InitSawActiveMutant string

func init() {
	InitSawActiveMutant = os.Getenv("GOMUTANT_ACTIVE_MUTANT")
}
`,
		"b.go": `package crossinit

type impl struct{}

func (impl) Val(x int) int {
	return x + 1
}
`,
		"cross_test.go": `package crossinit

import "testing"

func TestCrossFileInit(t *testing.T) {
	if InitSawActiveMutant != "" {
		t.Fatalf("a.go init() saw GOMUTANT_ACTIVE_MUTANT=%q, want empty", InitSawActiveMutant)
	}
	if GlobalVal != 11 {
		t.Fatalf("GlobalVal = %d, want 11", GlobalVal)
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gen, err := tr.CandidatesContext(ctx, "example.com/crossinit.impl.Val", 0)
	if err != nil {
		t.Fatal(err)
	}
	bounds := DeriveOracleBounds(-1, 1)
	defer bounds.Close()
	moduleDir, packageDir, err := tr.PackageContext("example.com/crossinit")
	if err != nil {
		t.Fatal(err)
	}
	env := tr.GoEnv()
	mutantsRan := 0
	for _, c := range gen.Candidates {
		m, ok := c.Mutant()
		if !ok {
			continue
		}
		if m.Operator == "statement: delete" {
			if m.SchemaMutantID != "" {
				t.Fatalf("expected missing-return candidate to be filtered from schema, got id=%q", m.SchemaMutantID)
			}
			continue
		}
		if m.SchemaMutantID == "" || len(m.SchemaReplacements) != 2 {
			t.Fatalf("candidate %s (%s): expected 2-file schema replacement (a.go init + b.go switch), got id=%q repls=%d\nsrc:\n%s",
				m.Position, m.Operator, m.SchemaMutantID, len(m.SchemaReplacements), string(m.Replacements[0].Source))
		}
		outcome, killer, _, obs, inc, _, err := RunMutantObservedEnv(
			ctx, dir, m, []string{"example.com/crossinit"}, "^TestCrossFileInit$", time.Minute,
			nil, moduleDir, packageDir, nil, nil, env, bounds,
		)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != MutantKilled || killer != "example.com/crossinit.TestCrossFileInit" {
			t.Fatalf("mutant %s (%s) outcome=(%v, %q), want (MutantKilled, example.com/crossinit.TestCrossFileInit)",
				m.Position, m.Operator, outcome, killer)
		}
		if inc != "" || !obs.OK || obs.Unverifiable {
			t.Fatalf("mutant %s observation incomplete=%q obs=%+v", m.Position, inc, obs)
		}
		mutantsRan++
	}
	if mutantsRan == 0 {
		t.Fatal("expected at least one mutant of impl.Val to run")
	}
}

func TestSchemaBinaryTimeoutAndPanicOnExit0(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test -c and schema binary execution")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/schematimeout\n\ngo 1.26.4\n",
		"p.go": `package schematimeout

import "os"

func CountUp(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		sum += i
	}
	return sum
}

func GuardExit(ok bool) int {
	if !ok {
		os.Exit(0)
	}
	return 1
}
`,
		"p_test.go": `package schematimeout

import "testing"

func TestCountUp(t *testing.T) {
	if CountUp(2) != 1 {
		t.Fatal("CountUp(2) != 1")
	}
}

func TestGuardExit(t *testing.T) {
	if GuardExit(true) != 1 {
		t.Fatal("GuardExit(true) != 1")
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	countGen, err := tr.CandidatesContext(ctx, "example.com/schematimeout.CountUp", 0)
	if err != nil {
		t.Fatal(err)
	}
	exitGen, err := tr.CandidatesContext(ctx, "example.com/schematimeout.GuardExit", 0)
	if err != nil {
		t.Fatal(err)
	}
	MergeCandidateSchemas(countGen.Candidates, exitGen.Candidates)

	bounds := DeriveOracleBounds(-1, 1)
	defer bounds.Close()
	moduleDir, packageDir, err := tr.PackageContext("example.com/schematimeout")
	if err != nil {
		t.Fatal(err)
	}
	env := tr.GoEnv()

	foundLoopTimeout := false
	for _, c := range countGen.Candidates {
		m, ok := c.Mutant()
		if !ok || m.Operator != "increment/decrement: ++ -> --" {
			continue
		}
		if m.SchemaMutantID == "" {
			t.Fatalf("expected incdec mutant to have SchemaMutantID")
		}
		outcome, killer, _, _, _, _, err := RunMutantObservedEnv(
			ctx, dir, m, []string{"example.com/schematimeout"}, "^TestCountUp$", 150*time.Millisecond,
			nil, moduleDir, packageDir, nil, nil, env, bounds,
		)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != MutantKilled || killer != TimeoutKiller {
			t.Fatalf("infinite-loop schema mutant outcome=(%v, %q), want (MutantKilled, %q)",
				outcome, killer, TimeoutKiller)
		}
		foundLoopTimeout = true
	}
	if !foundLoopTimeout {
		t.Fatal("did not find incdec infinite-loop mutant on CountUp")
	}

	foundExit0Kill := false
	for _, c := range exitGen.Candidates {
		m, ok := c.Mutant()
		if !ok || m.SchemaMutantID == "" {
			continue
		}
		if m.Operator != "condition: force true" {
			continue
		}
		outcome, killer, _, _, _, _, err := RunMutantObservedEnv(
			ctx, dir, m, []string{"example.com/schematimeout"}, "^TestGuardExit$", time.Minute,
			nil, moduleDir, packageDir, nil, nil, env, bounds,
		)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != MutantKilled {
			t.Fatalf("os.Exit(0)-triggering schema mutant %s (%s) outcome=(%v, %q), want MutantKilled",
				m.Position, m.Operator, outcome, killer)
		}
		foundExit0Kill = true
	}
	if !foundExit0Kill {
		t.Fatal("did not find os.Exit(0)-triggering mutant on GuardExit")
	}
}

func TestSchemaTopLevelHelpersSharedOverlayAndRogueWriteSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test -c and schema binary execution")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/sharedov\n\ngo 1.26.4\n",
		"lib/lib.go": `package lib

import (
	"os"
	"path/filepath"
)

func WriteToDir(outDir string) error {
	if outDir == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Join(outDir, "refs"), 0o500); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "HEAD"), []byte("ref"), 0o600)
}
`,
		"lib/lib_test.go": `package lib

import "testing"

func TestWriteToDirEmptyGuard(t *testing.T) {
	if err := WriteToDir(""); err == nil {
		t.Fatal("expected error for empty outDir")
	}
}
`,
		"consumer/consumer_test.go": `package consumer

import (
	"testing"

	"example.com/sharedov/lib"
)

func TestConsumerCallsWriteToDir(t *testing.T) {
	if err := lib.WriteToDir(""); err == nil {
		t.Fatal("expected error for empty outDir")
	}
}
`,
	}
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gen, err := tr.CandidatesContext(ctx, "example.com/sharedov/lib.WriteToDir", 0)
	if err != nil {
		t.Fatal(err)
	}
	var guardMutant Mutant
	foundGuardMutant := false
	for _, c := range gen.Candidates {
		m, ok := c.Mutant()
		if !ok || m.SchemaMutantID == "" {
			continue
		}
		schemaSrc := string(m.SchemaReplacements[0].Source)
		if !strings.Contains(schemaSrc, "_default(") {
			t.Fatalf("expected top-level _default helper function in schema source, got:\n%s", schemaSrc)
		}
		if (m.Operator == "condition: force false" || m.Operator == "comparison: == -> !=") && strings.Contains(c.Position, "lib.go:9:") {
			guardMutant = m
			foundGuardMutant = true
		}
	}
	if !foundGuardMutant {
		t.Fatal("expected condition: force false mutant on outDir == \"\" guard")
	}

	bounds := DeriveOracleBounds(-1, 2)
	defer bounds.Close()
	env := tr.GoEnv()
	moduleDir, libPkgDir, err := tr.PackageContext("example.com/sharedov/lib")
	if err != nil {
		t.Fatal(err)
	}
	_, consumerPkgDir, err := tr.PackageContext("example.com/sharedov/consumer")
	if err != nil {
		t.Fatal(err)
	}

	out1, killer1, _, obs1, inc1, _, err := RunMutantObservedEnv(
		ctx, dir, guardMutant, []string{"example.com/sharedov/lib"}, "^TestWriteToDirEmptyGuard$", time.Minute,
		nil, moduleDir, libPkgDir, nil, nil, env, bounds,
	)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != MutantKilled || killer1 != "example.com/sharedov/lib.TestWriteToDirEmptyGuard" {
		t.Fatalf("lib oracle outcome=(%v, %q), want killed by TestWriteToDirEmptyGuard", out1, killer1)
	}
	if inc1 != "" || !obs1.OK || obs1.Unverifiable {
		t.Fatalf("lib oracle observation after rogue mutant write: inc=%q obs=%+v", inc1, obs1)
	}
	if _, statErr := os.Stat(filepath.Join(libPkgDir, "HEAD")); !os.IsNotExist(statErr) {
		t.Fatalf("rogue mutant write HEAD leaked into libPkgDir: statErr=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(libPkgDir, "refs")); !os.IsNotExist(statErr) {
		t.Fatalf("rogue mutant write refs/ leaked into libPkgDir: statErr=%v", statErr)
	}

	out2, killer2, _, obs2, inc2, _, err := RunMutantObservedEnv(
		ctx, dir, guardMutant, []string{"example.com/sharedov/consumer"}, "^TestConsumerCallsWriteToDir$", time.Minute,
		nil, moduleDir, consumerPkgDir, nil, nil, env, bounds,
	)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != MutantKilled || killer2 != "example.com/sharedov/consumer.TestConsumerCallsWriteToDir" {
		t.Fatalf("consumer oracle outcome=(%v, %q), want killed by TestConsumerCallsWriteToDir", out2, killer2)
	}
	if inc2 != "" || !obs2.OK || obs2.Unverifiable {
		t.Fatalf("consumer oracle observation after rogue mutant write: inc=%q obs=%+v", inc2, obs2)
	}

	bounds.Schemas.mu.Lock()
	overlayCount := len(bounds.Schemas.overlays)
	entryCount := len(bounds.Schemas.entries)
	bounds.Schemas.mu.Unlock()
	if overlayCount != 1 || entryCount != 2 {
		t.Fatalf("SchemaCache overlays=%d (want 1 shared overlay), entries=%d (want 2 testPkg binaries)", overlayCount, entryCount)
	}
}

func TestSchemaParenthesizedGenericReceiverAndConcurrentPkgDirHygiene(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/parengen\n\ngo 1.26.4\n",
		"box.go": `package parengen

type Box[T comparable] struct {
	val T
}

func (_ *(Box[T])) Same(a, b T) bool {
	return a == b
}

func (_ (Box[T])) Echo(a, b T) bool {
	return a != b
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, sym := range []string{"example.com/parengen.Box.Same", "example.com/parengen.Box.Echo"} {
		gen, err := tr.CandidatesContext(ctx, sym, 0)
		if err != nil {
			t.Fatal(err)
		}
		sawSchema := false
		for _, c := range gen.Candidates {
			if c.SchemaMutantID != "" {
				sawSchema = true
			}
		}
		if !sawSchema {
			t.Fatalf("%s: expected parenthesized generic receiver candidates to compile in schema", sym)
		}
	}

	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "keep.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := NewSchemaCache()
	defer cache.Close()
	namespaces := []gofresh.ScratchNamespace{{Dir: ".", Pattern: "work-*"}}

	// Worker A enters pkgDir and writes rogue file "HEAD".
	leaveA := enterPkgDir(cache, pkgDir, pkgDir, namespaces)
	if err := os.WriteFile(filepath.Join(pkgDir, "HEAD"), []byte("rogue-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Worker B enters pkgDir while Worker A's "HEAD" is on disk, and creates a
	// legitimate ScratchNamespace entry "work-live".
	leaveB := enterPkgDir(cache, pkgDir, pkgDir, namespaces)
	scratchEntry := filepath.Join(pkgDir, "work-live")
	if err := os.WriteFile(scratchEntry, []byte("scratch-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Worker A finishes and sweeps: "HEAD" is removed, while Worker B's
	// ScratchNamespace entry "work-live" is preserved.
	leaveA()
	if _, err := os.Stat(filepath.Join(pkgDir, "HEAD")); !os.IsNotExist(err) {
		t.Fatalf("after Worker A sweep, HEAD stat err = %v, want IsNotExist", err)
	}
	if _, err := os.Stat(scratchEntry); err != nil {
		t.Fatalf("after Worker A sweep, ScratchNamespace entry work-live was deleted: %v", err)
	}
	// Worker B now writes another rogue "HEAD", removes its scratch entry, and leaves.
	if err := os.WriteFile(filepath.Join(pkgDir, "HEAD"), []byte("rogue-B"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(scratchEntry)
	leaveB()
	if _, err := os.Stat(filepath.Join(pkgDir, "HEAD")); !os.IsNotExist(err) {
		t.Fatalf("after Worker B sweep, HEAD leaked into pkgDir (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "keep.go")); err != nil {
		t.Fatalf("baseline file keep.go missing after sweep: %v", err)
	}
}
