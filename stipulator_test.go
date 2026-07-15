package gomutant

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/bindingsurface"
)

func TestParseStipulatorTargets(t *testing.T) {
	targets, err := ParseStipulatorTargets(stipulatorFixture(t, "valid", "full.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 {
		t.Fatalf("targets = %+v", targets)
	}
	empty, shared, second := targets[0], targets[1], targets[2]
	if empty.Symbol != "example.com/p.Empty" || !empty.OracleExplicit || empty.Oracle == nil || len(empty.Oracle) != 0 ||
		!slices.Equal(empty.Labels, []string{"REQ-fixture-empty", "stipulator:surface:3588efc6bb90a165eb3018df3d2f0dcbb89229660616dbd78170fb16da9f4f26"}) {
		t.Fatalf("empty target = %+v", empty)
	}
	if shared.Symbol != "example.com/p.F" || !shared.OracleExplicit ||
		!slices.Equal(shared.Oracle, []string{"example.com/p.TestA", "example.com/p.TestShared", "example.com/p.TestSharedRequirement"}) ||
		!slices.Equal(shared.Labels, []string{
			"REQ-fixture-alpha", "REQ-fixture-beta", "REQ-fixture-shared",
			"stipulator:surface:66615fc9d02c0ceb2c060af880948fd7af8f471393c77ca33a89c768c45d7651",
		}) {
		t.Fatalf("shared target = %+v", shared)
	}
	if second.Symbol != "example.com/p.G" || !slices.Equal(second.Oracle, []string{"example.com/p.TestSharedRequirement"}) {
		t.Fatalf("second implementation = %+v", second)
	}
}

func TestParseStipulatorEmptyAndUnicode(t *testing.T) {
	empty, err := ParseStipulatorTargets(stipulatorFixture(t, "valid", "empty.json"))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty report = %#v, %v", empty, err)
	}
	unicodeTargets, err := ParseStipulatorTargets(stipulatorFixture(t, "valid", "unicode.json"))
	if err != nil || len(unicodeTargets) != 1 || unicodeTargets[0].Symbol != "example.com/café.F" || unicodeTargets[0].Oracle[0] != "example.com/café.Test" {
		t.Fatalf("unicode report = %+v, %v", unicodeTargets, err)
	}
}

func TestParseStipulatorIncludesProvesOnlyOracle(t *testing.T) {
	binding := &bindingsurface.Binding{}
	binding.SetBackend("go")
	binding.SetRole(bindingsurface.BindingRoleProves)
	binding.SetSymbol("p.ProofF")
	surface := &bindingsurface.Surface{}
	surface.SetBackend("go")
	surface.SetSymbol("p.F")
	surface.SetRequirementIds([]string{"REQ-a"})
	surface.SetBindings([]*bindingsurface.Binding{binding})
	id, err := bindingsurface.Identifier(surface)
	if err != nil {
		t.Fatal(err)
	}
	surface.SetId(id)
	report := &bindingsurface.Report{}
	report.SetFormat(bindingsurface.Format)
	report.SetSurfaces([]*bindingsurface.Surface{surface})
	data, err := bindingsurface.MarshalJSON(report)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := ParseStipulatorTargets(data)
	if err != nil || len(targets) != 1 || !slices.Equal(targets[0].Oracle, []string{"p.ProofF"}) {
		t.Fatalf("proves-only oracle = %+v, %v", targets, err)
	}
}

func TestRejectsInvalidStipulatorFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/stipulator/v1/invalid/*.json")
	if err != nil {
		t.Fatal(err)
	}
	consumerPaths, err := filepath.Glob("testdata/stipulator/v1/gomutant-invalid/*.json")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, consumerPaths...)
	paths = append(paths, filepath.Join("testdata", "stipulator", "v1", "valid", "mixed-backend.json"))
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseStipulatorTargets(data); err == nil {
				t.Fatalf("invalid fixture accepted: %s", data)
			}
		})
	}
}

func TestRejectsEachIncompatibleStipulatorBackend(t *testing.T) {
	report, err := bindingsurface.ParseJSON(stipulatorFixture(t, "valid", "mixed-backend.json"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		surface   *bindingsurface.Surface
		wantError []string
	}{
		{
			name:      "binding backend",
			surface:   report.GetSurfaces()[0],
			wantError: []string{"2c794a900c1f1a01ed72fe8ec4786b9f97b0aadda85dc79d238649709cf6df05", "proto", "example.com/p.Check"},
		},
		{
			name:      "implementation backend",
			surface:   report.GetSurfaces()[1],
			wantError: []string{"d0513d42d6ba4fe61c4e3a17ea82eebd8643ccead8d41bb5844b971ac00c4cfd", "proto", "example.com/p.Proto"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report.SetSurfaces([]*bindingsurface.Surface{test.surface})
			data, err := bindingsurface.MarshalJSON(report)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseStipulatorTargets(data)
			if err == nil {
				t.Fatal("incompatible backend accepted")
			}
			for _, want := range test.wantError {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want substring %q", err, want)
				}
			}
		})
	}
}

func TestLoadTargetsRejectsLegacyStipulatorFormat(t *testing.T) {
	_, err := LoadTargets([]byte(`{"stipulatorTargets":1,"targets":[]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field stipulatorTargets") {
		t.Fatalf("legacy format error = %v", err)
	}
}

func stipulatorFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"testdata", "stipulator", "v1"}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
