package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The field reproducer for the pin-gated disposition carry: measure,
// attest, move the oracle's pins (edit the oracle test), re-measure - the run
// output and the document on disk agree that the disposition shed, and
// both say so loudly (REQ-attest-survivor, REQ-mcp-findings-doc).
func TestRunShedsRejectedDispositionInOutputAndDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	fixture := isolatedFixture(t)
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := runOptions{dir: fixture, targetsFile: targetsPath, findingsFile: defaultFindings, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute}

	var first bytes.Buffer
	opts.output = &first
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(fixture, defaultFindings)
	measured, err := loadFindings(fixture, docPath)
	if err != nil || len(measured) != 1 || len(measured[0].Survivors) == 0 {
		t.Fatalf("first measure = %+v, %v", measured, err)
	}
	survivor := measured[0].Survivors[0]

	var echo bytes.Buffer
	if err := attestCommand(ctx, attestOptions{
		dir: fixture, findingsFile: defaultFindings,
		symbol: measured[0].Symbol, position: survivor.Position, operator: survivor.Operator, reason: "equivalent by inspection",
	}, &echo); err != nil {
		t.Fatal(err)
	}
	// The disposition echo names what it did, the record's layer, and no
	// warning while the record can serve as it stands
	// (REQ-attest-survivor).
	if !strings.Contains(echo.String(), "attested "+survivor.Position) || !strings.Contains(echo.String(), "layer:") {
		t.Fatalf("attest echo = %q", echo.String())
	}
	if strings.Contains(echo.String(), "warning:") {
		t.Fatalf("current record warned: %q", echo.String())
	}

	// A different oracle timeout moves a measurement pin without
	// touching source geometry: the serve refuses, the record
	// re-measures in full with no in-run carry, and the survivor
	// re-appears at its exact position, operator, and site - the case
	// only the run-start snapshot can shed.
	opts.oracleTimeout = 3 * time.Minute

	var second bytes.Buffer
	opts.output = &second
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "attestation shed:") || !strings.Contains(second.String(), "pins moved") {
		t.Fatalf("re-measure output did not shed loudly:\n%s", second.String())
	}
	if !strings.Contains(second.String(), "0 attested") || strings.Contains(second.String(), "1 attested") {
		t.Fatalf("re-measure summary still counts the disposition:\n%s", second.String())
	}
	after, err := loadFindings(fixture, docPath)
	if err != nil || len(after) != 1 {
		t.Fatalf("re-measure document = %+v, %v", after, err)
	}
	if len(after[0].Attested) != 0 {
		t.Fatalf("rejected disposition re-attached on disk: %+v", after[0].Attested)
	}
	if len(after[0].Survivors) == 0 {
		t.Fatal("re-measure lost the survivor the shed disposition covered")
	}

	// Attesting against a record that can no longer serve as it stands
	// warns at disposition time - the next measure judges the
	// equivalence afresh (REQ-attest-survivor's echo clause). Editing
	// the oracle test's body makes the record inspect stale.
	libTest := filepath.Join(fixture, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(src), `t.Fatal("small arm")`, `t.Fatal("small arm moved")`, 1)
	if moved == string(src) {
		t.Fatal("TestWeak edit anchor missing")
	}
	if err := os.WriteFile(libTest, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened := after[0].Open()[0]
	var staleEcho bytes.Buffer
	if err := attestCommand(ctx, attestOptions{
		dir: fixture, findingsFile: defaultFindings,
		symbol: after[0].Symbol, position: reopened.Position, operator: reopened.Operator, reason: "equivalent by inspection",
	}, &staleEcho); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(staleEcho.String(), "warning: the record is stale") {
		t.Fatalf("birth-stale disposition did not warn: %q", staleEcho.String())
	}
}

// An attested survivor the strengthened oracle now kills is one story
// told once: the contradiction line - killed evidence with the shed
// reasoning attached - and never a second, vaguer merge-layer shed for
// the same mutant (REQ-attest-survivor).
func TestRunCommandContradictionIsTheSingleReport(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	fixture := isolatedFixture(t)
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := runOptions{dir: fixture, targetsFile: targetsPath, findingsFile: defaultFindings, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute}

	opts.output = &bytes.Buffer{}
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(fixture, defaultFindings)
	measured, err := loadFindings(fixture, docPath)
	if err != nil || len(measured) != 1 || len(measured[0].Survivors) == 0 {
		t.Fatalf("first measure = %+v, %v", measured, err)
	}
	survivor := measured[0].Survivors[0]
	if err := attestCommand(ctx, attestOptions{
		dir: fixture, findingsFile: defaultFindings,
		symbol: measured[0].Symbol, position: survivor.Position, operator: survivor.Operator, reason: "judged equivalent, wrongly",
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	// Strengthen the oracle to exercise the branch the survivor lives
	// in: the re-measure kills the attested mutant.
	libTest := filepath.Join(fixture, "lib", "lib_test.go")
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

	var second bytes.Buffer
	opts.output = &second
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "contradiction") {
		t.Fatalf("killed attested survivor produced no contradiction line:\n%s", second.String())
	}
	if strings.Contains(second.String(), "attestation shed: "+measured[0].Symbol+" "+survivor.Position) {
		t.Fatalf("contradicted disposition retold as a merge shed:\n%s", second.String())
	}
	after, err := loadFindings(fixture, docPath)
	if err != nil || len(after) != 1 || len(after[0].Attested) != 0 {
		t.Fatalf("contradicted disposition still on record: %+v, %v", after, err)
	}
}
