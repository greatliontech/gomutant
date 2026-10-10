package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stored moved-bracket clause follows the producer's root spelling at
// load and at the first committing write, without changing its reviewed
// subject or rationale. Repeated loads are a fixed point and collisions
// still refuse (REQ-result-exemptions).
func TestExemptionMovedBracketRootRekey(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, tc := range []struct {
		name, from, to string
	}{
		{"brackets", "fixtures [data]", `"fixtures [data]"`},
		{"separator", "fixtures; data", `"fixtures; data"`},
		{"quote", `fixtures"data`, `"fixtures\"data"`},
		{"absolute", "ROOT/fixtures [data]", `"fixtures [data]"`},
		{"quoted absolute", `"ROOT/fixtures [data]"`, `"fixtures [data]"`},
		{"quoted plain absolute", `"ROOT/fixtures"`, "fixtures"},
		{"embedded absolute", "other: ROOT/fixtures", `"other: ROOT/fixtures"`},
		{"canonical", `"fixtures [data]"`, `"fixtures [data]"`},
		{"literal quotes", `"fixtures"`, `"\"fixtures\""`},
		{"literal single quotes", "'x'", "'x'"},
		{"literal backticks", "`fixtures`", "`fixtures`"},
		{"plain", "fixtures", "fixtures"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "checkout [special]")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "findings.json")
			from := "observation bracket moved: " + strings.ReplaceAll(tc.from, "ROOT", filepath.ToSlash(dir))
			to := "observation bracket moved: " + strings.ReplaceAll(tc.to, "ROOT", filepath.ToSlash(dir))
			entry := Exemption{Subject: "p.ATest", Reason: from, Rationale: "reviewed root"}
			data, err := json.Marshal(exemptionsDocument{Version: 1, Exemptions: []Exemption{entry}})
			if err != nil {
				t.Fatal(err)
			}
			exemptionPath := ExemptionsPathFor(path)
			if err := os.WriteFile(exemptionPath, data, 0o644); err != nil {
				t.Fatal(err)
			}
			entries, rekeyed, err := LoadExemptions(exemptionPath, dir)
			if err != nil {
				t.Fatal(err)
			}
			want := entry
			want.Reason = to
			if len(entries) != 1 || entries[0] != want {
				t.Fatalf("loaded %+v, want %+v", entries, want)
			}
			if from != to {
				if len(rekeyed) != 1 || rekeyed[0] != (RekeyedExemption{Subject: entry.Subject, From: from, To: to}) {
					t.Fatalf("re-key account = %+v", rekeyed)
				}
			} else if len(rekeyed) != 0 {
				t.Fatalf("unchanged clause reported a re-key: %+v", rekeyed)
			}
			if matched := exemptionFor(entries, entry.Subject, to+" [added: child]"); matched == nil || *matched != want {
				t.Fatalf("canonical reason did not match: %+v", matched)
			}
			if tc.name == "quoted plain absolute" && exemptionFor(entries, entry.Subject, `observation bracket moved: "\"fixtures\"" [added: child]`) != nil {
				t.Fatal("syntax quotes were accepted as the relocated root's literal name")
			}
			if !strings.Contains(tc.from, "ROOT") {
				if matched := exemptionFor(entries, entry.Subject, from+" [added: child]"); matched == nil || *matched != want {
					t.Fatalf("stored older root spelling did not match: %+v", matched)
				}
			}
			got, err := os.ReadFile(exemptionPath)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("read-only load changed the record: %s (%v)", got, err)
			}
			store, err := OpenStore(path, dir)
			if err != nil {
				t.Fatal(err)
			}
			finding := storeFinding("p.A", func(f *Finding) {
				for _, e := range []*SubjectEvidence{&f.TargetEvidence, &f.OracleEvidence[0]} {
					e.RuntimeUnverifiable, e.RuntimeReason = true, to
				}
			})
			if layer, reason := store.Layer(finding); layer != LayerRepo {
				t.Fatalf("accepted finding routed %s: %s", layer, reason)
			}
			if _, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
				return []Finding{finding}, nil
			}); err != nil {
				t.Fatal(err)
			}
			entries, again, err := LoadExemptions(exemptionPath, dir)
			if err != nil || len(entries) != 1 || entries[0] != want || len(again) != 0 {
				t.Fatalf("persisted canonical record: %+v; re-key %+v; error %v", entries, again, err)
			}
		})
	}
	t.Run("collision", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "exemptions.json")
		data := `{"version":1,"exemptions":[{"subject":"p.T","reason":"observation bracket moved: fixtures [data]","rationale":"old"},{"subject":"p.T","reason":"observation bracket moved: \"fixtures [data]\"","rationale":"new"}]}`
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadExemptions(path, dir); err == nil || !strings.Contains(err.Error(), "entries 0 and 1 both accept p.T") || !strings.Contains(err.Error(), "entry 0 re-keyed from") {
			t.Fatalf("canonical collision = %v", err)
		}
	})
}

func TestExemptionEmbeddedPathAndQuotedPrefixResidual(t *testing.T) {
	dir := t.TempDir()
	for _, prefix := range []string{"external directory input: ", "observation bracket unverifiable: unhashable runtime input: "} {
		clause := prefix + "other: " + dir + "/fixtures"
		if got, changed := rekeyClause(clause, []string{dir}); changed || got != clause {
			t.Fatalf("embedded path was relocated: %q -> %q", clause, got)
		}
	}
	const canonical = `observation bracket moved: "\"fixtures\"suffix"`
	entries := []Exemption{{Subject: "p.T", Reason: canonical, Rationale: "reviewed"}}
	if exemptionFor(entries, "p.T", canonical+" [added: child]") == nil {
		t.Fatal("canonical quoted-prefix root did not match")
	}
	if exemptionFor(entries, "p.T", `observation bracket moved: "fixtures"suffix [added: child]`) != nil {
		t.Fatal("ambiguous legacy quoted-prefix reason granted an acceptance")
	}
	line := RekeyedExemptionsLine([]RekeyedExemption{{Subject: "p.T", From: "observation bracket moved: fixtures [data]", To: `observation bracket moved: "fixtures [data]"`}})
	const want = `re-keyed 1 reviewed exemption clause(s) to the canonical spelling: p.T "observation bracket moved: fixtures [data]" -> "observation bracket moved: \"fixtures [data]\""`
	if line != want {
		t.Fatalf("quoting-only account = %q, want %q", line, want)
	}
}
