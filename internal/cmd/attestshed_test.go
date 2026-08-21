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

// A disposition is a judgment about the mutated source, so it carries
// across moved measurement pins and sheds on a moved mutation domain:
// measure, attest, re-measure under a different oracle timeout - the
// disposition rides, reported distinctly - then edit the target's body
// below the survivor's site and re-measure - the disposition sheds,
// loudly, in output and document alike (REQ-attest-survivor,
// REQ-mcp-findings-doc).
func TestRunCarriesAcrossPinsAndShedsOnDomainMove(t *testing.T) {
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
	// re-measures in full, the survivor re-appears at its exact
	// position, operator, and site - and the disposition rides, with
	// the carry reported distinctly (the acceptance outliving the
	// environment it was judged in is auditable at the moment it rides).
	opts.oracleTimeout = 3 * time.Minute

	var second bytes.Buffer
	opts.output = &second
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "attestation carried: "+measured[0].Symbol+" "+survivor.Position) {
		t.Fatalf("re-measure under moved pins did not report the carry:\n%s", second.String())
	}
	if strings.Contains(second.String(), "attestation shed:") {
		t.Fatalf("re-measure under a held mutation domain shed the disposition:\n%s", second.String())
	}
	carried, err := loadFindings(fixture, docPath)
	if err != nil || len(carried) != 1 {
		t.Fatalf("re-measure document = %+v, %v", carried, err)
	}
	if len(carried[0].Attested) != 1 || carried[0].Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("disposition did not ride the pins-moved re-measure: %+v", carried[0].Attested)
	}

	// Editing the target's body moves the mutation domain: the judged
	// subject changed, so the disposition sheds - loudly - even though
	// the survivor re-appears at its exact position and operator. (In
	// this four-line fixture the survivor's site window overlaps the
	// edit, so the site cause may outrank the domain cause - the
	// domain-move reason itself is pinned by the merge-graft unit test;
	// here the pin is that a domain move sheds and says so.)
	libSrc := filepath.Join(fixture, "lib", "lib.go")
	body, err := os.ReadFile(libSrc)
	if err != nil {
		t.Fatal(err)
	}
	domainMoved := strings.Replace(string(body), "\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}", "\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x + 0\n}", 1)
	if domainMoved == string(body) {
		t.Fatal("Weak body edit anchor missing")
	}
	if err := os.WriteFile(libSrc, []byte(domainMoved), 0o644); err != nil {
		t.Fatal(err)
	}
	var third bytes.Buffer
	opts.output = &third
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(third.String(), "attestation shed: "+measured[0].Symbol+" "+survivor.Position) {
		t.Fatalf("domain-move re-measure did not shed loudly:\n%s", third.String())
	}
	if strings.Contains(third.String(), "attestation carried:") {
		t.Fatalf("domain-move re-measure carried the disposition:\n%s", third.String())
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

// Under a FULL re-measure (a moved pin beside the strengthened oracle),
// an attested mutant the re-execution kills contradicts its equivalence
// claim exactly as the drift serve's kill does: one contradiction line
// naming the killer, never a vaguer merge shed, and the disposition off
// the record (REQ-attest-survivor).
func TestRunFullRemeasureContradictionNamesKiller(t *testing.T) {
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
	// The moved oracle timeout beside the oracle edit forces the full
	// re-measure path rather than the killer-drift serve.
	opts.oracleTimeout = 3 * time.Minute
	var second bytes.Buffer
	opts.output = &second
	if err := runCommand(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "contradiction") || !strings.Contains(second.String(), "killed by") {
		t.Fatalf("full re-measure kill of an attested mutant produced no killer-naming contradiction:\n%s", second.String())
	}
	if strings.Contains(second.String(), "attestation shed: "+measured[0].Symbol+" "+survivor.Position) {
		t.Fatalf("contradicted disposition retold as a merge shed:\n%s", second.String())
	}
	after, err := loadFindings(fixture, docPath)
	if err != nil || len(after) != 1 || len(after[0].Attested) != 0 {
		t.Fatalf("contradicted disposition still on record: %+v, %v", after, err)
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
