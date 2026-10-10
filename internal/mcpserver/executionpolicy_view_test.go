package mcpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	gomutant "github.com/greatliontech/gomutant"
)

// Every recorded findings projection carries the policy gate without loading
// the tree or promoting an admissible policy to freshness (REQ-result-inspection,
// REQ-mcp-envelope).
func TestRecordedToolFindingsProjectOracleExecutionPolicy(t *testing.T) {
	const issue = "oracle execution policy is missing or unsupported; re-measure with the complete oracle"
	for _, tt := range []struct {
		name, policy, issue string
		shaped              bool
	}{
		{"current policy", "gomutant/full-oracle@1", "", false},
		{"legacy body", "", issue, false},
		{"unknown policy", "gomutant/future-oracle@999", issue, false},
		{"legacy shaped", "", "", true},
		{"unknown shaped", "gomutant/future-oracle@999", issue, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("not a module file\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			f := seededFinding("example.com/empty.Gone")
			f.OracleExecutionPolicy = tt.policy
			f.CandidateCount, f.Generated, f.Mutants, f.Killed = 3, 3, 3, 2
			f.Operators = []gomutant.OperatorSummary{{Operator: "zero return", Generated: 3, Killed: 2, Survived: 1}}
			f.Survivors = []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}
			if tt.shaped {
				f.Shape = &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
				f.TargetEvidence = gomutant.SubjectEvidence{}
			}
			path := gomutant.FindingsPathAt(dir, gomutant.DefaultFindingsPath)
			if err := gomutant.UpdateDocument(t.Context(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
				return []gomutant.Finding{f}, nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s := New(dir)
			for _, detail := range []bool{false, true} {
				_, out, err := s.toolFindings(t.Context(), nil, findingsIn{Detail: detail})
				if err != nil {
					t.Fatal(err)
				}
				var value any
				if detail {
					if len(out.Findings) != 1 || len(out.Summary) != 0 {
						t.Fatalf("detail rows: %+v", out)
					}
					value = out.Findings[0]
				} else {
					if len(out.Summary) != 1 || len(out.Findings) != 0 {
						t.Fatalf("summary rows: %+v", out)
					}
					value = out.Summary[0]
				}
				row := policyProjectionJSON(t, value, tt.policy, tt.issue)
				if row["state"] != "recorded" || row["reuse"] != nil {
					t.Fatalf("policy became a freshness verdict: %+v", row)
				}
				if detail {
					if row["killed"] != float64(2) || row["mutants"] != float64(3) || len(row["open"].([]any)) != 1 {
						t.Fatalf("historical detail counts changed: %+v", row)
					}
				} else if row["open"] != float64(1) || row["attested"] != float64(0) {
					t.Fatalf("historical summary counts changed: %+v", row)
				}
				if s.tree != nil {
					t.Fatal("recorded inspection loaded the tree")
				}
			}
			// Run rows use the same projection, with no new reuse verdict.
			rows, omitted, _, err := capRunFindings(t.Context(), []gomutant.Finding{f}, func(gomutant.Finding) (string, string) {
				return gomutant.LayerLocal, "dirty"
			}, nil)
			if err != nil || omitted != 0 || len(rows) != 1 {
				t.Fatalf("run rows = %+v, omitted=%d: %v", rows, omitted, err)
			}
			row := policyProjectionJSON(t, rows[0], tt.policy, tt.issue)
			if row["reuse"] != nil || row["killed"] != float64(2) {
				t.Fatalf("run projection derived a verdict or changed counts: %+v", row)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("recorded inspection rewrote the saved document: %v", err)
			}
		})
	}
}

func policyProjectionJSON(t *testing.T, value any, policy, issue string) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"oracleExecutionPolicy": policy, "oracleExecutionPolicyIssue": issue} {
		got, present := row[key]
		if (want == "" && present) || (want != "" && got != want) {
			t.Fatalf("%s = %v (present=%v), want %q: %s", key, got, present, want, data)
		}
	}
	return row
}

// Policy metadata does not expand the default row or survivor caps
// (REQ-mcp-envelope), and the projection leaves its input records untouched.
func TestOracleExecutionPolicyProjectionKeepsEnvelopeBounds(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	var findings []gomutant.Finding
	for i := 0; i < 53; i++ {
		f := seededFinding(fmt.Sprintf("example.com/empty.Gone%02d", i))
		f.OracleExecutionPolicy = ""
		findings = append(findings, f)
	}
	path := gomutant.FindingsPathAt(dir, gomutant.DefaultFindingsPath)
	if err := gomutant.UpdateDocument(t.Context(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return findings, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, detail := range []bool{false, true} {
		_, out, err := New(dir).toolFindings(t.Context(), nil, findingsIn{Detail: detail})
		if err != nil || len(out.Summary)+len(out.Findings) != 50 || out.Omitted != 3 {
			t.Fatalf("detail=%v: rows=%d, omitted=%d: %v", detail, len(out.Summary)+len(out.Findings), out.Omitted, err)
		}
	}
	for i := range findings {
		for j := 0; j < 23; j++ {
			findings[i].Survivors = append(findings[i].Survivors, gomutant.Survivor{Position: fmt.Sprintf("f.go:%d:1", j+1), Operator: "op"})
		}
	}
	before, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	rows, omitted, _, err := capRunFindings(t.Context(), findings, func(gomutant.Finding) (string, string) { return gomutant.LayerLocal, "dirty" }, nil)
	if err != nil || len(rows) != 50 || omitted != 3 {
		t.Fatalf("run rows=%d, omitted=%d: %v", len(rows), omitted, err)
	}
	for _, row := range rows {
		if len(row.Open) != 20 || row.OmittedOpen != 3 {
			t.Fatalf("run survivor cap: %+v", row)
		}
		policyProjectionJSON(t, row, "", "oracle execution policy is missing or unsupported; re-measure with the complete oracle")
	}
	after, err := json.Marshal(findings)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("run projection changed its input: %v", err)
	}
}

// The policy fields are flat optional properties of every served row, with
// their historical-count and non-freshness meaning stated in the schema
// (REQ-mcp-envelope).
func TestOracleExecutionPolicyRowSchemas(t *testing.T) {
	for _, tt := range []struct {
		name  string
		build func(*jsonschema.ForOptions) (*jsonschema.Schema, error)
	}{
		{"run", jsonschema.For[findingOut]},
		{"summary", jsonschema.For[findingSummary]},
		{"detail", jsonschema.For[inspectedFinding]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := tt.build(nil)
			if err != nil {
				t.Fatal(err)
			}
			for key, description := range map[string]string{
				"oracleExecutionPolicy":      "the recorded oracle execution policy; absent on legacy records, never a current-tree freshness claim",
				"oracleExecutionPolicyIssue": "the measurement policy's known limitation; when present the counts are historical, not complete-oracle evidence; absence only permits further evidence judgment",
			} {
				field := schema.Properties[key]
				if field == nil || field.Type != "string" || field.Description != description {
					t.Fatalf("schema property %s = %+v", key, field)
				}
				for _, required := range schema.Required {
					if required == key {
						t.Fatalf("optional policy property %s required", key)
					}
				}
			}
		})
	}
}
