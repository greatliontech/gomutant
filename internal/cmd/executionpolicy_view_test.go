package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// Recorded inspection exposes the policy gate without deriving freshness or
// upgrading historical counts (REQ-result-inspection).
func TestRecordedFindingsProjectOracleExecutionPolicy(t *testing.T) {
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
			// Any attempted tree load would refuse this module. The recorded
			// views must still answer, including the policy-admissible rows.
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("not a module file\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			f := seededLocalFinding("example.com/empty.Gone")
			f.OracleExecutionPolicy = tt.policy
			f.CandidateCount, f.Generated, f.Mutants, f.Killed = 3, 3, 3, 2
			f.Operators = []gomutant.OperatorSummary{{Operator: "zero return", Generated: 3, Killed: 2, Survived: 1}}
			if tt.shaped {
				f.Shape = &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
				f.TargetEvidence = gomutant.SubjectEvidence{}
			}
			path := gomutant.FindingsPathAt(dir, defaultFindings)
			if err := gomutant.UpdateDocument(t.Context(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
				return []gomutant.Finding{f}, nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, detail := range []bool{false, true} {
				var out bytes.Buffer
				if err := findingsCommand(t.Context(), findingsOptions{dir: dir, findingsFile: defaultFindings, detail: detail}, &out); err != nil {
					t.Fatal(err)
				}
				text := out.String()
				if !strings.Contains(text, "recorded  example.com/empty.Gone") || !strings.Contains(text, "1 open, 0 attested") || strings.Contains(text, "current  ") {
					t.Fatalf("detail=%v: not a recorded row: %s", detail, text)
				}
				if tt.issue != "" {
					line := fmt.Sprintf("    oracle execution policy %q: %s; counts are historical\n", tt.policy, tt.issue)
					if strings.Count(text, line) != 1 {
						t.Fatalf("detail=%v: missing exact policy limitation %q: %s", detail, line, text)
					}
					if detail && strings.Index(text, line) > strings.Index(text, "    survivor ") {
						t.Fatalf("policy limitation follows survivors: %s", text)
					}
				} else {
					if strings.Contains(text, "counts are historical") {
						t.Fatalf("policy-admissible record reported a limitation: %s", text)
					}
					if tt.policy != "" && strings.Count(text, fmt.Sprintf("    oracle execution policy %q\n", tt.policy)) != 1 {
						t.Fatalf("supported recorded policy missing: %s", text)
					}
					if tt.policy == "" && strings.Contains(text, "oracle execution policy") {
						t.Fatalf("missing historical policy was invented: %s", text)
					}
				}
				if detail && !strings.Contains(text, "3/3 candidates, 3 mutants, 2 killed, 0 discarded") {
					t.Fatalf("historical counts changed: %s", text)
				}
			}
			var out bytes.Buffer
			if err := findingsCommand(t.Context(), findingsOptions{dir: dir, findingsFile: defaultFindings, json: true}, &out); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 {
				t.Fatalf("JSON rows: %s (%v)", &out, err)
			}
			row := rows[0]
			if row["state"] != "recorded" || row["killed"] != float64(2) || row["mutants"] != float64(3) {
				t.Fatalf("JSON changed historical state/counts: %+v", row)
			}
			for key, want := range map[string]string{"oracleExecutionPolicy": tt.policy, "oracleExecutionPolicyIssue": tt.issue} {
				got, present := row[key]
				if (want == "" && present) || (want != "" && got != want) {
					t.Fatalf("JSON %s = %v (present=%v), want %q: %s", key, got, present, want, &out)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("recorded inspection rewrote the saved document: %v", err)
			}
		})
	}
}
