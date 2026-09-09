package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The heartbeat paces on the one cadence every face's progress keeps
// (the MCP heartbeat clause of REQ-mcp-envelope), and the envelope's
// bounds are the ones the requirement names.
func TestHeartbeatAndEnvelopeAreOnePolicyEach(t *testing.T) {
	if heartbeatInterval != gomutant.ProgressCadence {
		t.Fatalf("heartbeat cadence = %s; want the shared %s", heartbeatInterval, gomutant.ProgressCadence)
	}
	if envelope.rows != 50 {
		t.Fatalf("envelope rows = %d; want the requirement's 50", envelope.rows)
	}
	if envelope.open != 20 {
		t.Fatalf("envelope open survivors = %d; want the requirement's 20", envelope.open)
	}
	if envelope.nested <= 0 || envelope.nested > envelope.rows {
		t.Fatalf("envelope nested = %d; want a bound tighter than the %d rows", envelope.nested, envelope.rows)
	}
	if envelope.reasons <= 0 {
		t.Fatalf("envelope reasons = %d; want a positive bound", envelope.reasons)
	}
	if envelope.streamed < envelope.rows {
		t.Fatalf("envelope streamed = %d; want at least the %d rows", envelope.streamed, envelope.rows)
	}
}

// The findings response's ephemeral-attestation list is a response row
// list: it caps at the row bound with the remainder counted
// (REQ-mcp-envelope).
func TestFindingsEphemeralAttestationsCapAtTheRowBound(t *testing.T) {
	s := serverAt(t)
	path := gomutant.EphemeralAttestationsPathFor(gomutant.FindingsPathAt(s.dir, ""))
	for i := 0; i < envelope.rows+3; i++ {
		att := gomutant.EphemeralAttestation{EditDigest: fmt.Sprintf("d%d", i), RawEditDigest: fmt.Sprintf("r%d", i), Files: []string{"lib/lib.go"}, TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", Reason: "equivalent", Dirty: true}
		if err := gomutant.RecordEphemeralAttestation(context.Background(), path, att, false); err != nil {
			t.Fatal(err)
		}
	}
	_, out, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.EphemeralAttestations) != envelope.rows || out.OmittedEphemeralAttestations != 3 {
		t.Fatalf("ephemeral attestations = %d listed, %d omitted; want the %d row bound and 3 counted", len(out.EphemeralAttestations), out.OmittedEphemeralAttestations, envelope.rows)
	}
}

// The findings tool serves the document's coverage bounds beside the
// rows (REQ-result-unreached-bound).
func TestFindingsServesTheDocumentCoverageBounds(t *testing.T) {
	s := serverAt(t)
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, ""), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	// Rows and rosters cap at the envelope's row bound, the remainders
	// counted (REQ-mcp-envelope): envelope.rows+2 rows, one of them
	// carrying envelope.rows+3 symbols.
	roster := make([]string, 0, envelope.rows+3)
	for i := 0; i < envelope.rows+3; i++ {
		roster = append(roster, fmt.Sprintf("example.com/fixture/leg.F%03d", i))
	}
	store.RecordCoverageBound(gomutant.CoverageBound{Selection: "tags:wasm", Run: "r1", Unreached: roster})
	for i := 0; i < envelope.rows+1; i++ {
		store.RecordCoverageBound(gomutant.CoverageBound{Selection: fmt.Sprintf("toolchain:go1.%03d", i), Run: "r1", Unreached: []string{"example.com/fixture/leg.Dark"}})
	}
	if err := store.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	_, out, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.CoverageBounds) != envelope.rows || out.OmittedCoverageBounds != 2 {
		t.Fatalf("coverageBounds = %d rows, %d omitted; want the row bound and 2 counted", len(out.CoverageBounds), out.OmittedCoverageBounds)
	}
	var wasm *coverageBoundOut
	for i := range out.CoverageBounds {
		if out.CoverageBounds[i].Selection == "tags:wasm" {
			wasm = &out.CoverageBounds[i]
		}
	}
	if wasm == nil || len(wasm.Unreached) != envelope.rows || wasm.OmittedUnreached != 3 || wasm.Unreached[0] != "example.com/fixture/leg.F000" {
		t.Fatalf("wasm row = %+v, want its roster capped at the row bound with 3 counted", wasm)
	}
}

// The served run summary's unreached roster caps at the row bound with
// the remainder counted, while the document's bound carries the whole
// roster (REQ-mcp-envelope, REQ-result-unreached-bound).
func TestRunSummaryCapsTheUnreachedRoster(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	var leg strings.Builder
	leg.WriteString("//go:build seltag\n\npackage leg\n")
	for i := 0; i < envelope.rows+3; i++ {
		fmt.Fprintf(&leg, "\nfunc F%03d(x int) int { return x + %d }\n", i, i+1)
	}
	for name, content := range map[string]string{
		"go.mod":        "module example.com/sel\n\ngo 1.26\n",
		"pkg/f.go":      "package pkg\n\nfunc F(x int) int { return x + 1 }\n",
		"pkg/f_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fatal()\n\t}\n}\n",
		"leg/dark.go":   leg.String(),
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir)
	_, out, err := s.toolRun(context.Background(), nil, runIn{selectionIn: selectionIn{Tags: []string{"seltag"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.Selection != "tags:seltag" || len(out.Summary.Unreached) != envelope.rows || out.OmittedUnreached != 3 {
		t.Fatalf("summary = selection %q, %d unreached, %d omitted; want the row bound and 3 counted", out.Summary.Selection, len(out.Summary.Unreached), out.OmittedUnreached)
	}
	_, inspected, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected.CoverageBounds) != 1 || inspected.CoverageBounds[0].OmittedUnreached != 3 || len(inspected.CoverageBounds[0].Unreached) != envelope.rows {
		t.Fatalf("served bounds = %+v", inspected.CoverageBounds)
	}
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, ""), dir)
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := store.CoverageBounds(context.Background())
	if err != nil || len(bounds) != 1 || len(bounds[0].Unreached) != envelope.rows+3 {
		t.Fatalf("document bounds = %+v, %v; want the whole roster", bounds, err)
	}
	// A scoped run under the same selection states its bound and leaves
	// the document's row to the whole-tree run that recorded it.
	_, scoped, err := s.toolRun(context.Background(), nil, runIn{selectionIn: selectionIn{Tags: []string{"seltag"}}, Packages: []string{"example.com/sel/leg"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Summary.Unreached) != envelope.rows || scoped.OmittedUnreached != 3 {
		t.Fatalf("scoped summary = %d unreached, %d omitted", len(scoped.Summary.Unreached), scoped.OmittedUnreached)
	}
	after, err := store.CoverageBounds(context.Background())
	if err != nil || len(after) != 1 || after[0].Run != bounds[0].Run || after[0].Run == scoped.Summary.Run {
		t.Fatalf("scoped run touched the row: %+v (was %+v), %v", after, bounds, err)
	}
}

// A whole-tree run that discovers no targets still clears its
// selection's standing row through the reconcile's write
// (REQ-result-unreached-bound).
func TestZeroTargetWholeTreeRunClearsTheServedBound(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":  "module example.com/bare\n\ngo 1.26\n",
		"bare.go": "package bare\n\ntype T struct{}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir)
	store, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, ""), dir)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordCoverageBound(gomutant.CoverageBound{Selection: "tags:seltag", Run: "r-old", Unreached: []string{"example.com/bare/leg.Gone"}})
	if err := store.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.toolRun(context.Background(), nil, runIn{selectionIn: selectionIn{Tags: []string{"seltag"}}}); err != nil {
		t.Fatal(err)
	}
	after, err := gomutant.OpenStore(gomutant.FindingsPathAt(s.dir, ""), dir)
	if err != nil {
		t.Fatal(err)
	}
	if bounds, err := after.CoverageBounds(context.Background()); err != nil || len(bounds) != 0 {
		t.Fatalf("zero-target whole-tree run left the row: %+v, %v", bounds, err)
	}
}
