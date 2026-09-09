package gomutant

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The faces' shared mechanisms have one home: the findings path
// resolves under one rule and the target-source preamble names the
// given sources in the face's order.
func TestOneSourceHelpersForBothFaces(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "tree")
	for _, row := range []struct{ path, want string }{
		{"", filepath.Join(dir, filepath.FromSlash(DefaultFindingsPath))},
		{"out/f.json", filepath.Join(dir, "out", "f.json")},
		{filepath.Join(string(filepath.Separator), "abs", "f.json"), filepath.Join(string(filepath.Separator), "abs", "f.json")},
	} {
		if got := FindingsPathAt(dir, row.path); got != row.want {
			t.Fatalf("FindingsPathAt(%q) = %q, want %q", row.path, got, row.want)
		}
	}
	given := TargetSourcesGiven(TargetSource{Name: "--targets", Given: true}, TargetSource{Name: "--changed", Given: false}, TargetSource{Name: "--inline", Given: true})
	if !reflect.DeepEqual(given, []string{"--targets", "--inline"}) {
		t.Fatalf("TargetSourcesGiven = %v", given)
	}
	if got := TargetSourcesGiven(TargetSource{Name: "--targets"}); len(got) != 0 {
		t.Fatalf("nothing given rendered %v", got)
	}
}
